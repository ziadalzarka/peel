package tui_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
	"github.com/ziadalzarka/peel/internal/tui"
)

const (
	committedSvc = "package svc\n\nfunc Run() {\n\tdoWork()\n}\n"
	reviewedSvc  = "package svc\n\nfunc Run() {\n\tdoWork(ctx)\n}\n"
	// shiftedSvc is reviewedSvc with an import added, so doWork(ctx) is line 6.
	shiftedSvc = "package svc\n\nimport \"context\"\n\nfunc Run() {\n\tdoWork(ctx)\n}\n"
)

// openReview opens a backend on a repository whose one file is mid-change.
func openReview(t *testing.T) (*gittest.Repo, tui.Backend) {
	t.Helper()
	repo := gittest.New(t)
	repo.Write("svc.go", committedSvc)
	repo.Commit("initial")
	repo.Write("svc.go", reviewedSvc)

	a, err := app.Open(context.Background(), repo.Dir,
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	session, err := a.LoadWorkingTree(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	return repo, tui.NewBackend(a, session)
}

// codeUnderComment returns the diff line the first comment is drawn against.
//
// A comment carries the hunk it was placed in, and -1 when nothing claimed it —
// which is a note sitting under its file rather than on code. The row above is
// not the test: a note under the file trails the last line of the diff too.
func codeUnderComment(t *testing.T, doc tui.Document) string {
	t.Helper()
	for i, r := range doc.Rows {
		if r.Kind != tui.RowComment || !r.Head {
			continue
		}
		if r.Hunk < 0 {
			return "<under the file header>"
		}
		prev := doc.Rows[i-1]
		if prev.Kind != tui.RowLine {
			return "<not on a line>"
		}
		return doc.Hunks[prev.Hunk].Hunk.Lines[prev.Left].Render()
	}
	return "<not placed>"
}

func TestACommentStaysOnItsCodeWhenTheLinesShift(t *testing.T) {
	ctx := context.Background()
	repo, backend := openReview(t)

	// Written on doWork(ctx), which is line 4 as the reviewer sees it.
	if _, err := backend.AddComment(ctx, store.Comment{
		File: "svc.go", Line: 4, Side: store.SideNew, Origin: store.OriginWorktree,
		Body: "pass a real ctx", Author: store.AuthorUser,
	}); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// An agent adds an import above it while the review is open.
	repo.Write("svc.go", shiftedSvc)
	session, err := backend.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	comments, err := backend.Comments(ctx)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}

	doc := tui.Build(session, comments, nil, tui.LayoutUnified)
	if got := codeUnderComment(t, doc); !strings.Contains(got, "doWork(ctx)") {
		t.Errorf("the note is drawn against %q, want the doWork(ctx) it was written on", got)
	}
	if comments[0].Line != 6 {
		t.Errorf("line = %d, want 6", comments[0].Line)
	}
}

func TestACommentWhoseCodeIsGoneLandsUnderItsFile(t *testing.T) {
	ctx := context.Background()
	repo, backend := openReview(t)

	if _, err := backend.AddComment(ctx, store.Comment{
		File: "svc.go", Line: 4, Side: store.SideNew, Origin: store.OriginWorktree,
		Body: "pass a real ctx", Author: store.AuthorUser,
	}); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// The commented line is rewritten. There is no longer any code for the note
	// to be about, and line 4 now holds something the reviewer never read.
	repo.Write("svc.go", "package svc\n\nfunc Run() {\n\tdoNothing()\n}\n")
	session, err := backend.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	comments, err := backend.Comments(ctx)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if !comments[0].Outdated {
		t.Fatal("outdated = false, want the note to admit its code is gone")
	}

	doc := tui.Build(session, comments, nil, tui.LayoutUnified)
	if got := codeUnderComment(t, doc); got != "<under the file header>" {
		t.Errorf("the note is drawn against %q, want it parked under its file rather than on code it is not about", got)
	}
}

// Staging a file takes the half a note was written on off the screen and puts
// the same code on the other one. The note has to go with it: parked under its
// file, it reads as a note about code nobody can point at, which is the failure
// the anchor exists to prevent wearing the reviewer's own decision as a cause.
func TestACommentFollowsItsFileIntoTheIndex(t *testing.T) {
	ctx := context.Background()
	_, backend := openReview(t)

	if _, err := backend.AddComment(ctx, store.Comment{
		File: "svc.go", Line: 4, Side: store.SideNew, Origin: store.OriginWorktree,
		Body: "pass a real ctx", Author: store.AuthorUser,
	}); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	if err := backend.StageFile(ctx, "svc.go"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	session, comments := reread(t, backend)
	if comments[0].Outdated {
		t.Fatal("staging outdated the note; nothing about the code changed")
	}

	doc := tui.Build(session, comments, nil, tui.LayoutUnified)
	if got := codeUnderComment(t, doc); !strings.Contains(got, "doWork(ctx)") {
		t.Errorf("the note is drawn against %q, want the doWork(ctx) it was written on", got)
	}

	// And back again: the reviewer who changes their mind gets their note back
	// exactly as it was, not a second copy of it under a header.
	if err := backend.UnstageFile(ctx, "svc.go"); err != nil {
		t.Fatalf("UnstageFile: %v", err)
	}
	session, comments = reread(t, backend)
	doc = tui.Build(session, comments, nil, tui.LayoutUnified)
	if got := codeUnderComment(t, doc); !strings.Contains(got, "doWork(ctx)") {
		t.Errorf("after unstaging the note is drawn against %q, want doWork(ctx)", got)
	}
}

// The same the other way round: a note written while the file was staged, on a
// file the reviewer then takes back out of the index.
func TestACommentFollowsItsFileBackOutOfTheIndex(t *testing.T) {
	ctx := context.Background()
	_, backend := openReview(t)

	if err := backend.StageFile(ctx, "svc.go"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if _, err := backend.Reload(ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if _, err := backend.AddComment(ctx, store.Comment{
		File: "svc.go", Line: 4, Side: store.SideNew, Origin: store.OriginIndex,
		Body: "pass a real ctx", Author: store.AuthorUser,
	}); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	if err := backend.UnstageFile(ctx, "svc.go"); err != nil {
		t.Fatalf("UnstageFile: %v", err)
	}
	session, comments := reread(t, backend)
	if comments[0].Outdated {
		t.Fatal("unstaging outdated the note; the code it is about is still on disk")
	}

	doc := tui.Build(session, comments, nil, tui.LayoutUnified)
	if got := codeUnderComment(t, doc); !strings.Contains(got, "doWork(ctx)") {
		t.Errorf("the note is drawn against %q, want the doWork(ctx) it was written on", got)
	}
}

// reread is what the UI does after a change lands: the session and its notes,
// read back from git.
func reread(t *testing.T, backend tui.Backend) (*app.Session, []store.Comment) {
	t.Helper()
	ctx := context.Background()
	session, err := backend.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	comments, err := backend.Comments(ctx)
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	return session, comments
}
