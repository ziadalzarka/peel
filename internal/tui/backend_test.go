package tui_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
	"github.com/ziadalzarka/peel/internal/tui"
)

// openBackend builds a real App over a scratch repository and returns a backend
// on its working tree.
func openBackend(t *testing.T) (*app.App, *app.Session, tui.Backend) {
	t.Helper()
	return backendOver(t, func(repo *gittest.Repo) {
		repo.Write("main.go", "package main\n\nfunc main() {}\n")
		repo.Commit("initial")
		repo.Write("main.go", "package main\n\nfunc main() { println(1) }\n")
	})
}

// backendOver is openBackend over a repository the test lays out itself, for
// the shapes one changed file cannot show.
func backendOver(t *testing.T, setup func(*gittest.Repo)) (*app.App, *app.Session, tui.Backend) {
	t.Helper()
	repo := gittest.New(t)
	setup(repo)

	// No providers: a test must never shell out to claude or gh.
	a, err := app.Open(context.Background(), repo.Dir,
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("app.Open: %v", err)
	}
	session, err := a.LoadWorkingTree(context.Background())
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	return a, session, tui.NewBackend(a, session)
}

func TestBackendStagesAFileAndReloadShowsIt(t *testing.T) {
	ctx := context.Background()
	_, session, backend := openBackend(t)

	entry := session.Files[0]
	if entry.Unstaged == nil || len(entry.Unstaged.Hunks) == 0 {
		t.Fatalf("expected an unstaged change, got %+v", entry)
	}

	if err := backend.StageFile(ctx, entry.Path); err != nil {
		t.Fatalf("StageFile: %v", err)
	}

	reloaded, err := backend.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if len(reloaded.Files) != 1 {
		t.Fatalf("files = %d, want 1", len(reloaded.Files))
	}
	if got := reloaded.Files[0].State(); got != git.StateStaged {
		t.Errorf("state = %v, want staged", got)
	}
}

func TestBackendUnstageFileEmptiesTheIndex(t *testing.T) {
	ctx := context.Background()
	_, session, backend := openBackend(t)
	path := session.Files[0].Path

	if err := backend.StageFile(ctx, path); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if err := backend.UnstageFile(ctx, path); err != nil {
		t.Fatalf("UnstageFile: %v", err)
	}

	reloaded, err := backend.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := reloaded.Files[0].State(); got != git.StateUnstaged {
		t.Errorf("state = %v, want unstaged", got)
	}
}

func TestBackendStageAllThenUnstageAll(t *testing.T) {
	ctx := context.Background()
	_, _, backend := openBackend(t)

	if err := backend.StageAll(ctx); err != nil {
		t.Fatalf("StageAll: %v", err)
	}
	staged, err := backend.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := staged.Files[0].State(); got != git.StateStaged {
		t.Fatalf("after StageAll state = %v, want staged", got)
	}

	if err := backend.UnstageAll(ctx); err != nil {
		t.Fatalf("UnstageAll: %v", err)
	}
	unstaged, err := backend.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := unstaged.Files[0].State(); got != git.StateUnstaged {
		t.Errorf("after UnstageAll state = %v, want unstaged", got)
	}
}

// A pull request is not checked out here, so every staging operation must refuse
// rather than write to an unrelated index.
func TestBackendRefusesToStageAReadOnlySession(t *testing.T) {
	ctx := context.Background()
	a, _, _ := openBackend(t)

	session := &app.Session{
		Target:    "github:cli/cli#412",
		Title:     "cli/cli#412 fix the thing",
		Stageable: false,
		PR:        &forge.PullRequest{Ref: forge.Ref{Owner: "cli", Repo: "cli", Number: 412}},
	}
	backend := tui.NewBackend(a, session)

	ops := map[string]func() error{
		"StageFile":   func() error { return backend.StageFile(ctx, "main.go") },
		"UnstageFile": func() error { return backend.UnstageFile(ctx, "main.go") },
		"StageAll":    func() error { return backend.StageAll(ctx) },
		"UnstageAll":  func() error { return backend.UnstageAll(ctx) },
	}
	for name, op := range ops {
		err := op()
		if err == nil {
			t.Errorf("%s succeeded on a read-only session", name)
			continue
		}
		if !strings.Contains(err.Error(), "not in this working tree") {
			t.Errorf("%s error = %v, want it to explain why", name, err)
		}
	}
}

// Hiding the agent's review is written to peel's own state, so the next backend
// over the same repository opens the diff the way the last one left it — and one
// review's filter is not the next review's.
func TestBackendRemembersHiddenAgentComments(t *testing.T) {
	a, session, backend := openBackend(t)

	hidden, err := backend.AgentCommentsHidden()
	if err != nil {
		t.Fatalf("AgentCommentsHidden: %v", err)
	}
	if hidden {
		t.Fatal("a review nobody has filtered opens with the agent's notes hidden")
	}

	if err := backend.SetAgentCommentsHidden(true); err != nil {
		t.Fatalf("SetAgentCommentsHidden: %v", err)
	}

	again := tui.NewBackend(a, session)
	hidden, err = again.AgentCommentsHidden()
	if err != nil {
		t.Fatalf("AgentCommentsHidden: %v", err)
	}
	if !hidden {
		t.Error("the working tree opened showing notes it was left without")
	}

	pr := tui.NewBackend(a, &app.Session{Target: "github:cli/cli#412", Title: "pr"})
	hidden, err = pr.AgentCommentsHidden()
	if err != nil {
		t.Fatalf("AgentCommentsHidden: %v", err)
	}
	if hidden {
		t.Error("the working tree's filter reached a pull request")
	}
}

// Reloading a pull request would refetch a diff that cannot have changed, and
// would lose the target the comments are scoped to.
func TestBackendReloadOfAPullRequestIsAPassthrough(t *testing.T) {
	a, _, _ := openBackend(t)

	session := &app.Session{
		Target:    "github:cli/cli#412",
		Title:     "pr",
		Stageable: false,
		PR:        &forge.PullRequest{Ref: forge.Ref{Owner: "cli", Repo: "cli", Number: 412}},
	}
	backend := tui.NewBackend(a, session)

	got, err := backend.Reload(context.Background())
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got != session {
		t.Error("reloading a pull request returned a different session")
	}
}

// A revision session's far side is the working tree, so it goes on changing and
// follow mode must keep re-reading it — the passthrough is for pull requests
// only.
func TestBackendReloadOfARevisionFollowsTheWorkingTree(t *testing.T) {
	ctx := context.Background()
	repo := gittest.New(t)
	repo.Write("main.go", "package main\n")
	repo.Commit("initial")
	repo.Write("committed.go", "package main\n")
	repo.Commit("second")

	a, err := app.Open(ctx, repo.Dir,
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("app.Open: %v", err)
	}
	session, err := a.LoadRevision(ctx, "HEAD~1")
	if err != nil {
		t.Fatalf("LoadRevision: %v", err)
	}
	backend := tui.NewBackend(a, session)

	repo.Write("later.go", "package main\n")
	reloaded, err := backend.Reload(ctx)
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if _, ok := reloaded.Entry("later.go"); !ok {
		t.Errorf("reload missed a new file: %v", reloaded.Paths())
	}
	// Still measured from the pinned base, not from HEAD.
	if _, ok := reloaded.Entry("committed.go"); !ok {
		t.Errorf("reload lost the committed change: %v", reloaded.Paths())
	}
	if reloaded.Base != session.Base || reloaded.Title != session.Title {
		t.Errorf("reload = base %q title %q, want %q and %q",
			reloaded.Base, reloaded.Title, session.Base, session.Title)
	}
	if reloaded.Stageable {
		t.Error("Stageable = true after reloading a revision session")
	}
}

func TestBackendCommentsAreScopedToTheSession(t *testing.T) {
	a, session, backend := openBackend(t)

	if _, err := backend.AddComment(t.Context(), store.Comment{
		File:   "main.go",
		Line:   3,
		Side:   store.SideNew,
		Body:   "on the working tree",
		Author: store.AuthorUser,
	}); err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	// A comment on another target must not show up in this session.
	if _, err := a.Comments.Add(store.Comment{
		File:   "main.go",
		Line:   3,
		Body:   "on a pull request",
		Author: store.AuthorAgent,
		Target: "github:cli/cli#412",
	}); err != nil {
		t.Fatalf("Add on another target: %v", err)
	}

	got, err := backend.Comments(t.Context())
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("comments = %d, want only this session's", len(got))
	}
	if got[0].Body != "on the working tree" || got[0].Target != session.Target {
		t.Errorf("comment = %+v", got[0])
	}
}

func TestBackendStampsTheSessionTargetOnNewComments(t *testing.T) {
	a, _, _ := openBackend(t)

	session := &app.Session{Target: "github:cli/cli#412", Title: "pr", Stageable: false}
	backend := tui.NewBackend(a, session)

	// A caller that forgets the target, or sets the wrong one, must not be able
	// to file a comment against another review.
	got, err := backend.AddComment(t.Context(), store.Comment{File: "main.go", Body: "note", Target: "somewhere-else"})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}
	if got.Target != "github:cli/cli#412" {
		t.Errorf("target = %q, want the session's", got.Target)
	}
}

func TestBackendResolveAndRemoveComments(t *testing.T) {
	_, _, backend := openBackend(t)

	created, err := backend.AddComment(t.Context(), store.Comment{File: "main.go", Body: "look", Author: store.AuthorUser})
	if err != nil {
		t.Fatalf("AddComment: %v", err)
	}

	if err := backend.SetResolved(created.ID, true); err != nil {
		t.Fatalf("SetResolved: %v", err)
	}
	got, err := backend.Comments(t.Context())
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(got) != 1 || !got[0].Resolved {
		t.Fatalf("comments = %+v, want one resolved", got)
	}

	if err := backend.RemoveComment(t.Context(), created.ID); err != nil {
		t.Fatalf("RemoveComment: %v", err)
	}
	got, err = backend.Comments(t.Context())
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("comments = %+v, want none", got)
	}
}

func TestBackendWalkthroughReportsAMissingProvider(t *testing.T) {
	_, _, backend := openBackend(t)

	_, err := backend.Walkthrough(context.Background(), false)
	if err == nil {
		t.Fatal("Walkthrough succeeded with no AI provider registered")
	}
	if !strings.Contains(err.Error(), "provider") {
		t.Errorf("error = %v, want it to mention the missing provider", err)
	}
}

// contextOf reads the copies of the files behind a session, failing the test if
// the read itself fails.
func contextOf(t *testing.T, backend tui.Backend, s *app.Session) map[tui.FileSide][]string {
	t.Helper()
	files, err := backend.Context(context.Background(), s)
	if err != nil {
		t.Fatalf("Context: %v", err)
	}
	return files
}

// The code a hunk leaves out is read straight from the working tree, whole —
// not the three lines of context the diff carries.
func TestBackendReadsTheWorkingTreeBehindTheDiff(t *testing.T) {
	_, session, backend := openBackend(t)

	files := contextOf(t, backend, session)
	lines, ok := files[tui.FileSide{Path: "main.go"}]
	if !ok {
		t.Fatalf("nothing was read for main.go, got %v", files)
	}
	want := []string{"package main", "", "func main() { println(1) }"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("lines = %q, want %q", lines, want)
	}
	if _, ok := files[tui.FileSide{Path: "main.go", Staged: true}]; ok {
		t.Error("a file with nothing staged had a staged half read for it")
	}
}

// A part-staged file is two files on screen, numbered against two different
// copies: the index's for the staged half, the disk's for the rest. Reading the
// wrong one puts code beside a hunk that was never there.
func TestBackendReadsEachHalfOfAPartStagedFile(t *testing.T) {
	_, session, backend := backendOver(t, func(repo *gittest.Repo) {
		repo.Write("notes.txt", "one\ntwo\nthree\n")
		repo.Commit("initial")
		repo.Write("notes.txt", "one\nstaged\nthree\n")
		repo.Git("add", "notes.txt")
		repo.Write("notes.txt", "one\nstaged\nthree\non disk\n")
	})
	if got := session.Files[0].State(); got != git.StatePartial {
		t.Fatalf("state = %v, want part staged", got)
	}

	files := contextOf(t, backend, session)
	staged := strings.Join(files[tui.FileSide{Path: "notes.txt", Staged: true}], "|")
	if staged != "one|staged|three" {
		t.Errorf("staged half read %q, want the index's copy one|staged|three", staged)
	}
	working := strings.Join(files[tui.FileSide{Path: "notes.txt"}], "|")
	if working != "one|staged|three|on disk" {
		t.Errorf("working half read %q, want the disk's copy", working)
	}
}

// A pull request's files are not in this working tree. A local file at the same
// path is a different file, and reading it would put unrelated code in the diff.
func TestBackendReadsNothingBehindAPullRequest(t *testing.T) {
	a, _, _ := openBackend(t)

	session := &app.Session{
		Target:    "github:cli/cli#412",
		Title:     "cli/cli#412 fix the thing",
		Stageable: false,
		PR:        &forge.PullRequest{Ref: forge.Ref{Owner: "cli", Repo: "cli", Number: 412}},
		Files:     []git.FileEntry{{Path: "main.go"}},
	}
	backend := tui.NewBackend(a, session)

	if files := contextOf(t, backend, session); len(files) != 0 {
		t.Errorf("a pull request read %v, want nothing", files)
	}
}

// A binary file has no lines to read and no hunks to read them around.
func TestBackendReadsNothingBehindABinaryFile(t *testing.T) {
	binary := []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00, 0x42}
	_, session, backend := backendOver(t, func(repo *gittest.Repo) {
		repo.WriteBytes("img.bin", binary)
		repo.Commit("initial")
		repo.WriteBytes("img.bin", append(binary, 0x99, 0x00, 0x11))
	})

	if files := contextOf(t, backend, session); len(files) != 0 {
		t.Errorf("a binary file read %v, want nothing", files)
	}
}

// A deleted file has nothing on disk to read. That is one file peel cannot
// offer to open up, not a failed review.
func TestBackendReadsPastAFileThatIsGone(t *testing.T) {
	_, session, backend := backendOver(t, func(repo *gittest.Repo) {
		repo.Write("gone.txt", "one\ntwo\n")
		repo.Write("kept.go", "package main\n\nfunc main() {}\n")
		repo.Commit("initial")
		repo.Remove("gone.txt")
		repo.Write("kept.go", "package main\n\nfunc main() { println(1) }\n")
	})

	files := contextOf(t, backend, session)
	if _, ok := files[tui.FileSide{Path: "gone.txt"}]; ok {
		t.Error("a deleted file was read off disk")
	}
	if _, ok := files[tui.FileSide{Path: "kept.go"}]; !ok {
		t.Errorf("the file still there was not read, got %v", files)
	}
}
