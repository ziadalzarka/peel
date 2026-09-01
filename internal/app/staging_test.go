package app_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/exec"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

// Staging a file moves its change out of the working tree and into the index,
// and unstaging moves it back. Neither rewrites a line, so neither can outdate
// a note — but each does take the half the note was written on off the screen
// and put the same code on the other one, under different numbering. These are
// the four ways across: a note on either half, carried either way.

// noteOn writes a comment on one half of svc.go, anchored the way the UI would
// anchor one written there.
func noteOn(t *testing.T, a *app.App, s *app.Session, origin store.Origin, side store.Side, line int, body string) store.Comment {
	t.Helper()
	c := store.Comment{
		File: "svc.go", Line: line, Side: side, Origin: origin,
		Body: body, Author: store.AuthorUser,
	}
	blob, err := a.Snapshot(context.Background(), s, c)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if blob == "" {
		t.Fatal("no snapshot was taken; the note has nothing to be measured from")
	}
	c.Blob = blob
	created, err := a.Local.Comments.Add(c)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return created
}

// reread returns the session and its notes as the review would read them now.
func reread(t *testing.T, a *app.App) (*app.Session, []store.Comment) {
	t.Helper()
	ctx := context.Background()
	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	all, err := a.Local.Comments.List(store.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return s, a.Relocate(ctx, s, all)
}

func TestUnstagingKeepsANoteOnWhatWasTheStagedHalf(t *testing.T) {
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nCHANGED\nd\n")
	repo.Git("add", "svc.go")

	s, _ := reread(t, a)
	noteOn(t, a, s, store.OriginIndex, store.SideNew, 3, "note on the staged CHANGED")

	repo.Git("restore", "--staged", "svc.go")

	_, got := reread(t, a)
	if got[0].Outdated {
		t.Fatal("CHANGED is still on disk; unstaging cannot outdate a note about it")
	}
	if got[0].Line != 3 || got[0].MovedFrom != 0 {
		t.Errorf("note is on line %d (from %d), want 3 and unmoved", got[0].Line, got[0].MovedFrom)
	}
	if got[0].Origin != store.OriginWorktree {
		t.Errorf("origin = %q, want worktree — the change is out of the index now", got[0].Origin)
	}
}

func TestStagingKeepsANoteOnALineTheChangeRemoves(t *testing.T) {
	a, repo := anchorRepo(t, "a\nb\nGONE\nd\n", "a\nb\nd\n")

	s, _ := reread(t, a)
	// The old side of the unstaged diff is the index, not the commit.
	noteOn(t, a, s, store.OriginWorktree, store.SideOld, 3, "why is GONE going?")

	repo.Git("add", "svc.go")

	_, got := reread(t, a)
	if got[0].Outdated {
		t.Fatal("GONE is still what HEAD holds; staging cannot outdate a note about it")
	}
	if got[0].Line != 3 {
		t.Errorf("line = %d, want the 3 GONE sits on in the old side", got[0].Line)
	}
	if got[0].Origin != store.OriginIndex {
		t.Errorf("origin = %q, want index — the removal is staged now", got[0].Origin)
	}
}

// The note's code has not moved and was never going to; what moved is the half
// of the file the review draws it on. A note left on a half nothing draws hangs
// off no line at all, which is the same loss as being outdated wearing a
// different face.
func TestStagingCarriesANoteOntoTheHalfTheReviewDraws(t *testing.T) {
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nCHANGED\nd\n")

	s, _ := reread(t, a)
	noteOn(t, a, s, store.OriginWorktree, store.SideNew, 3, "note on CHANGED")

	repo.Git("add", "svc.go")

	s, got := reread(t, a)
	if entry, _ := s.Entry("svc.go"); entry.Unstaged != nil {
		t.Fatal("the fixture left something unstaged; this test is not exercising anything")
	}
	if got[0].Outdated || got[0].Line != 3 {
		t.Errorf("note is on line %d, outdated %v; want line 3 and current", got[0].Line, got[0].Outdated)
	}
	if got[0].Origin != store.OriginIndex {
		t.Errorf("origin = %q, want index — that is the only half left to draw it on", got[0].Origin)
	}
}

func TestUnstagingCarriesANoteOnARemovedLineBack(t *testing.T) {
	a, repo := anchorRepo(t, "a\nb\nGONE\nd\n", "a\nb\nd\n")
	repo.Git("add", "svc.go")

	s, _ := reread(t, a)
	// The old side of the staged diff is the commit.
	noteOn(t, a, s, store.OriginIndex, store.SideOld, 3, "why is GONE going?")

	repo.Git("restore", "--staged", "svc.go")

	_, got := reread(t, a)
	if got[0].Outdated {
		t.Fatal("GONE is back in the index; the note must not read as outdated")
	}
	if got[0].Line != 3 {
		t.Errorf("line = %d, want 3 — where GONE sits on the old side", got[0].Line)
	}
	if got[0].Origin != store.OriginWorktree {
		t.Errorf("origin = %q, want worktree — the removal is unstaged again", got[0].Origin)
	}
}

// Staging and unstaging are each other's undo, so a note pressed through both
// has to come back reading exactly as it did — including the half it names,
// since the note carried across is the same note and not a copy left behind.
func TestStagingAndUnstagingLeaveANoteExactlyWhereItWas(t *testing.T) {
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nCHANGED\nd\n")

	s, _ := reread(t, a)
	noteOn(t, a, s, store.OriginWorktree, store.SideNew, 3, "note on CHANGED")
	_, before := reread(t, a)

	repo.Git("add", "svc.go")
	reread(t, a)
	repo.Git("restore", "--staged", "svc.go")
	_, after := reread(t, a)

	if after[0] != before[0] {
		t.Errorf("the note came back as %+v, want the %+v it went in as", after[0], before[0])
	}
}

// A part-staged file is the case the move is not free: the index held less than
// the disk does, so the note's line really is somewhere else afterwards and the
// diff between the two is what says where.
func TestUnstagingAPartStagedFileFollowsTheLineOntoTheDisk(t *testing.T) {
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nc\nd\n")
	repo.Write("svc.go", "a\nb\nCHANGED\nd\n")
	repo.Git("add", "svc.go")
	// More arrives on disk, above the staged line.
	repo.Write("svc.go", "TOP\na\nb\nCHANGED\nd\n")

	s, _ := reread(t, a)
	noteOn(t, a, s, store.OriginIndex, store.SideNew, 3, "note on the staged CHANGED")

	repo.Git("restore", "--staged", "svc.go")

	_, got := reread(t, a)
	if got[0].Outdated {
		t.Fatal("CHANGED is still on disk; the note must not read as outdated")
	}
	if got[0].Line != 4 || got[0].MovedFrom != 3 {
		t.Errorf("note is on line %d (from %d), want line 4 from 3 — where CHANGED sits on disk",
			got[0].Line, got[0].MovedFrom)
	}
}

// The rescue must not become a second place to look for a line that is really
// gone, or outdated stops meaning anything.
func TestUnstagingStillOutdatesANoteWhoseCodeWasRewritten(t *testing.T) {
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nCHANGED\nd\n")
	repo.Git("add", "svc.go")

	s, _ := reread(t, a)
	noteOn(t, a, s, store.OriginIndex, store.SideNew, 3, "note on the staged CHANGED")

	// Unstaged, and the line it was about rewritten in the same breath.
	repo.Git("restore", "--staged", "svc.go")
	repo.Write("svc.go", "a\nb\nREWRITTEN\nd\n")

	_, got := reread(t, a)
	if !got[0].Outdated {
		t.Error("CHANGED is gone from both halves, and the note reads as current")
	}
	if got[0].Line != 3 {
		t.Errorf("line = %d, want the 3 it was written on left alone", got[0].Line)
	}
}

// An ordinary edit is not a staging move. The index holds what the file used to
// say, so a note whose line has just been rewritten would find its own content
// sitting there — and following it would send the note to a half nobody was
// reviewing it on, to say it is fine.
func TestAnEditIsNotAMoveAcrossTheIndex(t *testing.T) {
	a, repo := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nc\nd\n")

	s, _ := reread(t, a)
	noteOn(t, a, s, store.OriginWorktree, store.SideNew, 3, "note on c")

	repo.Write("svc.go", "a\nb\nREWRITTEN\nd\n")

	_, got := reread(t, a)
	if !got[0].Outdated {
		t.Error("c was rewritten and the note reads as current")
	}
	if got[0].Origin != store.OriginWorktree {
		t.Errorf("origin = %q, want worktree — an edit moves nothing across the index", got[0].Origin)
	}
}

// A caller can name a half the review never drew — an agent writing against the
// index on a file nobody staged. Sending that note to the half that is drawn
// would measure it against a copy that does not hold it, which is rewriting an
// anchor rather than following one.
func TestANoteNamingAHalfNobodyDrewIsLeftOnIt(t *testing.T) {
	a, _ := anchorRepo(t, "a\nb\nc\nd\n", "a\nb\nCHANGED\nd\n")

	s, _ := reread(t, a)
	noteOn(t, a, s, store.OriginIndex, store.SideNew, 3, "on the index, of a file nobody staged")

	_, got := reread(t, a)
	if got[0].Origin != store.OriginIndex {
		t.Errorf("origin = %q, want the index the note named", got[0].Origin)
	}
	if got[0].Outdated || got[0].Line != 3 {
		t.Errorf("note is on line %d, outdated %v; want the line 3 the index still holds",
			got[0].Line, got[0].Outdated)
	}
}

// countingRunner records the git subcommands peel issues.
type countingRunner struct {
	inner exec.Runner
	mu    sync.Mutex
	calls []string
}

func (c *countingRunner) Run(ctx context.Context, cmd exec.Command) (exec.Result, error) {
	c.mu.Lock()
	if len(cmd.Args) > 0 {
		c.calls = append(c.calls, cmd.Args[0])
	}
	c.mu.Unlock()
	return c.inner.Run(ctx, cmd)
}

func (c *countingRunner) since(mark int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls[mark:]...)
}

func (c *countingRunner) mark() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// A review re-reads its notes on every keypress and, in follow mode, on a timer.
// Asking git where each note sits one note at a time made that a pair of
// processes per commented file — so unstaging thirty files spawned sixty of
// them, in a row, before the screen could settle. What it costs now does not
// grow with the review.
func TestRelocateAsksGitAFixedNumberOfTimes(t *testing.T) {
	ctx := context.Background()
	repo := gittest.New(t)
	const files = 20
	body := ""
	for i := range 40 {
		body += fmt.Sprintf("line %d\n", i)
	}
	for i := range files {
		repo.Write(fmt.Sprintf("pkg/f%02d.go", i), body)
	}
	repo.Commit("base")
	for i := range files {
		repo.Write(fmt.Sprintf("pkg/f%02d.go", i), body+"tail\n")
	}
	repo.Git("add", "--all")

	run := &countingRunner{inner: exec.NewOSRunner()}
	a, err := app.Open(ctx, repo.Dir,
		app.WithRunner(run),
		app.WithAIRegistry(ai.NewRegistry()),
		app.WithForgeRegistry(forge.NewRegistry()),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	s, err := a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	for i := range files {
		c := store.Comment{
			File: fmt.Sprintf("pkg/f%02d.go", i), Line: 41, Side: store.SideNew,
			Origin: store.OriginIndex, Body: "note", Author: store.AuthorUser,
		}
		blob, err := a.Snapshot(ctx, s, c)
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		c.Blob = blob
		if _, err := a.Local.Comments.Add(c); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	all, _ := a.Local.Comments.List(store.Filter{})

	// Nothing has changed: every copy still holds what its note was written on,
	// so the two calls that establish that are the whole cost.
	mark := run.mark()
	if got := a.Relocate(ctx, s, all); got[0].Outdated {
		t.Fatal("a note went outdated with nothing changed")
	}
	if calls := run.since(mark); len(calls) > 2 {
		t.Errorf("re-reading %d notes over an unchanged tree ran %d git commands %v, want at most 2",
			files, len(calls), calls)
	}

	// Everything is unstaged at once, which is where this used to hurt most:
	// every note's half goes at the same moment.
	repo.Git("restore", "--staged", ".")
	s, err = a.LoadWorkingTree(ctx)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	mark = run.mark()
	got := a.Relocate(ctx, s, all)
	calls := run.since(mark)
	for _, c := range got {
		if c.Outdated {
			t.Fatalf("unstaging outdated a note on %s, whose code is still on disk", c.File)
		}
	}
	if len(calls) > 2 {
		t.Errorf("unstaging %d commented files ran %d git commands %v, want at most 2",
			files, len(calls), calls)
	}
}
