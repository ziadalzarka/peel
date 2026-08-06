package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/exec"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

// fakeAI is an ai.Provider whose output tests control.
type fakeAI struct {
	name      string
	available bool
	body      string
	err       error
	requests  []ai.Request
}

func (f *fakeAI) Name() string        { return f.name }
func (f *fakeAI) Description() string { return "fake AI provider" }

func (f *fakeAI) Available(context.Context) bool { return f.available }

func (f *fakeAI) Walkthrough(_ context.Context, req ai.Request) (string, error) {
	f.requests = append(f.requests, req)
	return f.body, f.err
}

// fakeForge is a forge.Provider whose behaviour tests control.
type fakeForge struct {
	name      string
	available bool
	pr        *forge.PullRequest
	parseErr  error
	fetchErr  error
	submitErr error
	submitted []forge.Review
}

func (f *fakeForge) Name() string        { return f.name }
func (f *fakeForge) Description() string { return "fake forge" }

func (f *fakeForge) Available(context.Context) bool { return f.available }

func (f *fakeForge) Parse(_ context.Context, _, ref string) (forge.Ref, error) {
	if f.parseErr != nil {
		return forge.Ref{}, f.parseErr
	}
	return forge.Ref{Owner: "o", Repo: "r", Number: 412}, nil
}

func (f *fakeForge) Fetch(context.Context, forge.Ref) (*forge.PullRequest, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return f.pr, nil
}

func (f *fakeForge) SubmitReview(_ context.Context, _ forge.Ref, r forge.Review) error {
	if f.submitErr != nil {
		return f.submitErr
	}
	f.submitted = append(f.submitted, r)
	return nil
}

// fixture is a repository plus the App opened on it.
type fixture struct {
	t    *testing.T
	repo *gittest.Repo
	app  *app.App
	ai   *fakeAI
	gh   *fakeForge
	ctx  context.Context
}

func newFixture(t *testing.T, opts ...app.Option) *fixture {
	t.Helper()
	repo := gittest.New(t)
	aiProvider := &fakeAI{name: "fake-ai", available: true, body: "## What changed\n\nStuff."}
	forgeProvider := &fakeForge{
		name:      "fake-forge",
		available: true,
		pr: &forge.PullRequest{
			Ref:    forge.Ref{Owner: "o", Repo: "r", Number: 412},
			Title:  "Drop the document key",
			Author: "ziad",
			Diff:   "diff --git a/pr.go b/pr.go\n--- a/pr.go\n+++ b/pr.go\n@@ -1 +1 @@\n-old\n+new\n",
		},
	}

	all := append([]app.Option{
		app.WithAIRegistry(ai.NewRegistry(aiProvider)),
		app.WithForgeRegistry(forge.NewRegistry(forgeProvider)),
	}, opts...)

	a, err := app.Open(context.Background(), repo.Dir, all...)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return &fixture{t: t, repo: repo, app: a, ai: aiProvider, gh: forgeProvider, ctx: context.Background()}
}

func TestOpenDiscoversRepository(t *testing.T) {
	f := newFixture(t)

	if f.app.Root == "" {
		t.Error("Root is empty")
	}
	if !strings.HasSuffix(f.app.StateDir, filepath.Join(".git", app.StateDirName)) {
		t.Errorf("StateDir = %q, want it inside .git", f.app.StateDir)
	}
}

func TestOpenFromSubdirectoryUsesRoot(t *testing.T) {
	repo := gittest.New(t)
	repo.Write("nested/deep/f.txt", "x\n")
	repo.Commit("base")

	a, err := app.Open(context.Background(), filepath.Join(repo.Dir, "nested", "deep"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Paths in diffs are root-relative, so commands must run from the root.
	if filepath.Base(a.Root) == "deep" {
		t.Errorf("Root = %q, want the repository root", a.Root)
	}
}

// Outside a repository there is no working tree to review, but a pull request
// is reviewable from anywhere — so peel opens, and only the session that needs
// a repository says it cannot be had.
func TestOpenOutsideRepositoryDefersToTheSession(t *testing.T) {
	t.Setenv("PEEL_STATE_DIR", t.TempDir())
	dir := t.TempDir()

	a, err := app.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open outside a repository: %v", err)
	}
	if a.HasRepo() {
		t.Error("HasRepo is true outside a repository")
	}
	if a.StateDir != "" {
		t.Errorf("StateDir = %q, want none without a git directory", a.StateDir)
	}

	_, err = a.LoadWorkingTree(context.Background())
	if err == nil {
		t.Fatal("loaded a working tree outside a repository")
	}
	if !strings.Contains(err.Error(), "git repository") || !strings.Contains(err.Error(), "--pr") {
		t.Errorf("error = %v, want it to explain the requirement and the way round it", err)
	}
}

func TestStateDirIsNotCreatedUntilNeeded(t *testing.T) {
	// Opening peel must not write to .git; only storing something should.
	f := newFixture(t)
	if _, err := os.Stat(f.app.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("StateDir exists after Open (err=%v), want it created lazily", err)
	}

	if _, err := f.app.Local.Comments.Add(store.Comment{File: "f.txt", Body: "note"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := os.Stat(f.app.StateDir); err != nil {
		t.Errorf("StateDir missing after a write: %v", err)
	}
}

func TestLoadWorkingTree(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "two\n")
	f.repo.Write("b.txt", "new\n")

	s, err := f.app.LoadWorkingTree(f.ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}

	if !s.Stageable {
		t.Error("Stageable = false for the working tree")
	}
	if s.Target != "" {
		t.Errorf("Target = %q, want empty for the working tree", s.Target)
	}
	if got := s.Paths(); len(got) != 2 {
		t.Errorf("Paths() = %v, want two files", got)
	}
	if !strings.Contains(s.DiffText, "two") {
		t.Errorf("DiffText missing the change:\n%s", s.DiffText)
	}
	if s.IsEmpty() {
		t.Error("IsEmpty() = true with changes present")
	}
}

func TestLoadWorkingTreeClean(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")

	s, err := f.app.LoadWorkingTree(f.ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	if !s.IsEmpty() {
		t.Errorf("IsEmpty() = false on a clean tree, got %v", s.Paths())
	}
}

func TestSessionStats(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\ntwo\nthree\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "one\nCHANGED\nthree\nfour\n")

	s, err := f.app.LoadWorkingTree(f.ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	added, removed := s.Stats()
	if added != 2 || removed != 1 {
		t.Errorf("Stats() = +%d -%d, want +2 -1", added, removed)
	}
}

// revisionFixture leaves one committed change and one uncommitted change on top
// of it, so a revision session and the working-tree session disagree.
func revisionFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "two\n")
	f.repo.Write("committed.txt", "landed\n")
	f.repo.Commit("second")
	f.repo.Write("a.txt", "three\n")
	return f
}

func TestLoadRevisionReachesPastTheLastCommit(t *testing.T) {
	f := revisionFixture(t)

	s, err := f.app.LoadRevision(f.ctx, "HEAD~1")
	if err != nil {
		t.Fatalf("LoadRevision: %v", err)
	}

	// The point of a base behind HEAD: work already committed is in the diff.
	want := []string{"a.txt", "committed.txt"}
	if got := s.Paths(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Paths() = %v, want %v", got, want)
	}
	// Both the committed change and the one on top of it, in one diff.
	if !strings.Contains(s.DiffText, "landed") || !strings.Contains(s.DiffText, "three") {
		t.Errorf("DiffText missing the full range:\n%s", s.DiffText)
	}
	if s.Stageable {
		t.Error("Stageable = true against a base behind HEAD")
	}
	if s.Target != "" {
		t.Errorf("Target = %q, want the working tree's, so comments stay in one place", s.Target)
	}
	if s.Title != "HEAD~1..working tree" {
		t.Errorf("Title = %q", s.Title)
	}
}

// The base is resolved once, so a commit landing mid-session cannot slide the
// diff out from under the reviewer.
func TestLoadRevisionPinsTheBase(t *testing.T) {
	f := revisionFixture(t)

	s, err := f.app.LoadRevision(f.ctx, "HEAD~1")
	if err != nil {
		t.Fatalf("LoadRevision: %v", err)
	}
	pinned := f.repo.Git("rev-parse", "HEAD~1")
	if s.Base != pinned {
		t.Errorf("Base = %q, want the resolved hash %q", s.Base, pinned)
	}

	f.repo.Commit("third")
	again, err := f.app.LoadRevision(f.ctx, s.Base)
	if err != nil {
		t.Fatalf("LoadRevision by hash: %v", err)
	}
	if again.Base != pinned {
		t.Errorf("Base = %q after a commit landed, want %q", again.Base, pinned)
	}
	if got := again.Paths(); len(got) != 2 {
		t.Errorf("Paths() = %v, want the same two files", got)
	}
}

// HEAD is the working tree's own base, and the only one staging can mean
// anything against.
func TestLoadRevisionOfHeadIsTheWorkingTree(t *testing.T) {
	f := revisionFixture(t)

	for _, ref := range []string{"HEAD", f.repo.Git("rev-parse", "HEAD")} {
		s, err := f.app.LoadRevision(f.ctx, ref)
		if err != nil {
			t.Fatalf("LoadRevision(%q): %v", ref, err)
		}
		if !s.Stageable {
			t.Errorf("LoadRevision(%q) is not stageable, want the working tree session", ref)
		}
		if s.Base != "" || s.Title != "working tree" {
			t.Errorf("LoadRevision(%q) = base %q title %q, want the working tree session", ref, s.Base, s.Title)
		}
	}
}

func TestLoadRevisionEmptyRefIsTheWorkingTree(t *testing.T) {
	f := revisionFixture(t)

	s, err := f.app.LoadRevision(f.ctx, "")
	if err != nil {
		t.Fatalf("LoadRevision: %v", err)
	}
	if !s.Stageable || s.Title != "working tree" {
		t.Errorf("LoadRevision(\"\") = %q, want the working tree session", s.Title)
	}
}

func TestLoadRevisionIncludesUntrackedFiles(t *testing.T) {
	f := revisionFixture(t)
	f.repo.Write("brand-new.txt", "hello\n")

	s, err := f.app.LoadRevision(f.ctx, "HEAD~1")
	if err != nil {
		t.Fatalf("LoadRevision: %v", err)
	}
	entry, ok := s.Entry("brand-new.txt")
	if !ok {
		t.Fatalf("untracked file missing from %v", s.Paths())
	}
	if !entry.Untracked || entry.Primary() == nil {
		t.Error("untracked file has no diff to review")
	}
}

func TestLoadRevisionUnknownRef(t *testing.T) {
	f := revisionFixture(t)

	_, err := f.app.LoadRevision(f.ctx, "no-such-ref")
	if err == nil {
		t.Fatal("LoadRevision succeeded on an unknown revision")
	}
	if !strings.Contains(err.Error(), "no-such-ref") {
		t.Errorf("error = %v, want it to name the revision", err)
	}
}

func TestLoadPullRequest(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}

	// A pull request is not in this working tree, so nothing can be staged.
	if s.Stageable {
		t.Error("Stageable = true for a pull request")
	}
	if s.Target != "fake-forge:o/r#412" {
		t.Errorf("Target = %q", s.Target)
	}
	if s.PR == nil {
		t.Fatal("PR is nil")
	}
	if got := s.Paths(); len(got) != 1 || got[0] != "pr.go" {
		t.Errorf("Paths() = %v, want [pr.go]", got)
	}
	if !strings.Contains(s.Title, "Drop the document key") {
		t.Errorf("Title = %q", s.Title)
	}
}

func TestLoadPullRequestPropagatesErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*fakeForge)
	}{
		{"parse fails", func(f *fakeForge) { f.parseErr = errors.New("bad ref") }},
		{"fetch fails", func(f *fakeForge) { f.fetchErr = errors.New("404") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			tt.setup(f.gh)
			if _, err := f.app.LoadPullRequest(f.ctx, "", "412"); err == nil {
				t.Fatal("LoadPullRequest succeeded, want an error")
			}
		})
	}
}

func TestLoadPullRequestUnavailableProvider(t *testing.T) {
	f := newFixture(t)
	f.gh.available = false

	_, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err == nil {
		t.Fatal("LoadPullRequest succeeded with no available forge")
	}
}

func TestWalkthroughGeneratesAndCaches(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "two\n")

	s, err := f.app.LoadWorkingTree(f.ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}

	got, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{})
	if err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got.Body != "## What changed\n\nStuff." {
		t.Errorf("Body = %q", got.Body)
	}
	if got.Provider != "fake-ai" {
		t.Errorf("Provider = %q", got.Provider)
	}

	// A second call for the same diff must come from the cache.
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{}); err != nil {
		t.Fatalf("Walkthrough (cached): %v", err)
	}
	if len(f.ai.requests) != 1 {
		t.Errorf("provider called %d times, want 1 — the cache was not used", len(f.ai.requests))
	}
}

func TestWalkthroughRegenerates(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "two\n")

	s, _ := f.app.LoadWorkingTree(f.ctx)
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{Regenerate: true}); err != nil {
		t.Fatalf("Walkthrough(regenerate): %v", err)
	}
	if len(f.ai.requests) != 2 {
		t.Errorf("provider called %d times, want 2", len(f.ai.requests))
	}
}

func TestWalkthroughInvalidatedByNewChanges(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "two\n")

	s, _ := f.app.LoadWorkingTree(f.ctx)
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}

	// The diff moves on; the cached narrative no longer describes it.
	f.repo.Write("a.txt", "three\nfour\n")
	s2, _ := f.app.LoadWorkingTree(f.ctx)
	if _, err := f.app.Walkthrough(f.ctx, s2, app.WalkthroughRequest{}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if len(f.ai.requests) != 2 {
		t.Errorf("provider called %d times, want 2 — a stale narrative was served", len(f.ai.requests))
	}
}

func TestWalkthroughRequestCarriesContext(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "two\n")

	s, _ := f.app.LoadWorkingTree(f.ctx)
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{Instruction: "be brief"}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}

	req := f.ai.requests[0]
	if req.Instruction != "be brief" {
		t.Errorf("Instruction = %q", req.Instruction)
	}
	if len(req.Files) != 1 || req.Files[0] != "a.txt" {
		t.Errorf("Files = %v", req.Files)
	}
	if !strings.Contains(req.Diff, "two") {
		t.Error("Diff does not contain the change")
	}
}

func TestWalkthroughEmptyDiff(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadWorkingTree(f.ctx)
	_, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{})
	if err == nil {
		t.Fatal("Walkthrough succeeded on a clean tree")
	}
	if len(f.ai.requests) != 0 {
		t.Error("a provider was called with nothing to summarise")
	}
}

func TestWalkthroughProviderFailureIsNotCached(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "two\n")
	f.ai.err = errors.New("not authenticated")

	s, _ := f.app.LoadWorkingTree(f.ctx)
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{}); err == nil {
		t.Fatal("Walkthrough succeeded despite a provider failure")
	}
	if _, ok, _ := f.app.Local.Walkthroughs.Load(); ok {
		t.Error("a failed generation was written to the cache")
	}
}

func TestWalkthroughForPullRequestNamesIt(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{}); err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got := f.ai.requests[0].Target; !strings.Contains(got, "o/r#412") {
		t.Errorf("Target = %q, want it to name the pull request", got)
	}
}

func TestCommentsAreScopedToTarget(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")
	f.repo.Write("f.txt", "y\n")

	working, _ := f.app.LoadWorkingTree(f.ctx)
	pr, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}

	mustAddComment(t, f, store.Comment{File: "f.txt", Line: 1, Body: "working tree note"})
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "pr note", Target: pr.Target})

	wt, err := f.app.Local.Comments.List(working.CommentFilter())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(wt) != 1 || wt[0].Body != "working tree note" {
		t.Errorf("working tree comments = %v", wt)
	}

	prComments, err := f.app.StateFor(pr).Comments.List(pr.CommentFilter())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(prComments) != 1 || prComments[0].Body != "pr note" {
		t.Errorf("PR comments = %v", prComments)
	}

	// The two are not only filtered apart: a pull request's notes are not in the
	// repository at all, which is what lets them be read from anywhere else.
	inRepo, err := f.app.Local.Comments.List(store.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(inRepo) != 1 || inRepo[0].Body != "working tree note" {
		t.Errorf("comments in the repository = %v, want the working tree's alone", inRepo)
	}
}

func TestSubmitReview(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "this leaks the tx", Target: s.Target})
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 2, Body: "and this", Target: s.Target, Side: store.SideOld})

	opts := app.SubmitOptions{Body: "a couple of things", Event: forge.EventRequestChanges}
	if _, err := f.app.SubmitReview(f.ctx, s, opts); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}

	if len(f.gh.submitted) != 1 {
		t.Fatalf("submitted %d reviews, want 1", len(f.gh.submitted))
	}
	got := f.gh.submitted[0]
	if got.Event != forge.EventRequestChanges {
		t.Errorf("Event = %q", got.Event)
	}
	if len(got.Comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(got.Comments))
	}
	if got.Comments[0].Side != "RIGHT" {
		t.Errorf("comment 0 Side = %q", got.Comments[0].Side)
	}
	if got.Comments[1].Side != "LEFT" {
		t.Errorf("comment 1 Side = %q, want the old side mapped to LEFT", got.Comments[1].Side)
	}
}

func TestSubmitReviewDefaultsToComment(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "note", Target: s.Target})

	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if got := f.gh.submitted[0].Event; got != forge.EventComment {
		t.Errorf("Event = %q, want COMMENT by default", got)
	}
}

func TestSubmitReviewExcludesOtherTargets(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "for the PR", Target: s.Target})
	mustAddComment(t, f, store.Comment{File: "f.txt", Line: 1, Body: "for the working tree"})

	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	got := f.gh.submitted[0].Comments
	if len(got) != 1 || got[0].Body != "for the PR" {
		t.Errorf("submitted %v, want only the PR's comments", got)
	}
}

func TestSubmitReviewExcludesResolved(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	done := mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "already handled", Target: s.Target})
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 2, Body: "still open", Target: s.Target})

	if _, err := f.app.StateFor(s).Comments.Update(done.ID, func(c *store.Comment) { c.Resolved = true }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}

	got := f.gh.submitted[0].Comments
	if len(got) != 1 || got[0].Body != "still open" {
		t.Errorf("submitted %v, want only unresolved comments", got)
	}
}

func TestSubmitReviewResolvesAfter(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "note", Target: s.Target})

	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{ResolveAfter: true}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}

	filter := s.CommentFilter()
	filter.Unresolved = true
	remaining, err := f.app.Local.Comments.List(filter)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d comments still unresolved after submission", len(remaining))
	}
}

func TestSubmitReviewFileLevelCommentsCannotBeInline(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	mustAddComment(t, f, store.Comment{File: "pr.go", Body: "general thought", Target: s.Target})

	_, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{})
	if err == nil {
		t.Fatal("SubmitReview succeeded with only a file-level comment")
	}
	if !strings.Contains(err.Error(), "--body") {
		t.Errorf("error = %v, want it to suggest a review body", err)
	}
	if len(f.gh.submitted) != 0 {
		t.Error("a review was posted despite the error")
	}
}

func TestSubmitReviewFileLevelCommentWithBodySucceeds(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	mustAddComment(t, f, store.Comment{File: "pr.go", Body: "general thought", Target: s.Target})

	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{Body: "overall looks fine"}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if got := f.gh.submitted[0].Body; got != "overall looks fine" {
		t.Errorf("Body = %q", got)
	}
}

func TestSubmitReviewNeedsAPullRequest(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")
	f.repo.Write("f.txt", "y\n")

	s, _ := f.app.LoadWorkingTree(f.ctx)
	_, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{Body: "x"})
	if err == nil {
		t.Fatal("SubmitReview succeeded for the working tree")
	}
	if !strings.Contains(err.Error(), "--pr") {
		t.Errorf("error = %v, want it to point at PR mode", err)
	}
}

func TestSubmitReviewNothingToSay(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{}); err == nil {
		t.Fatal("SubmitReview succeeded with no comments and no body")
	}
	if len(f.gh.submitted) != 0 {
		t.Error("an empty review was posted")
	}
}

func TestPreviewSubmissionDoesNotPost(t *testing.T) {
	// Preview is what makes confirmation meaningful, so it must not send.
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "note", Target: s.Target})

	got, err := f.app.PreviewSubmission(s, app.SubmitOptions{Body: "summary"})
	if err != nil {
		t.Fatalf("PreviewSubmission: %v", err)
	}
	if len(got.Comments) != 1 || got.Body != "summary" {
		t.Errorf("preview = %+v", got)
	}
	if len(f.gh.submitted) != 0 {
		t.Error("PreviewSubmission posted a review")
	}
}

func TestSubmitReviewFailureLeavesCommentsUnresolved(t *testing.T) {
	f := newFixture(t)
	f.repo.Write("f.txt", "x\n")
	f.repo.Commit("base")
	f.gh.submitErr = errors.New("HTTP 422")

	s, _ := f.app.LoadPullRequest(f.ctx, "", "412")
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "note", Target: s.Target})

	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{ResolveAfter: true}); err == nil {
		t.Fatal("SubmitReview succeeded despite a forge failure")
	}

	filter := s.CommentFilter()
	filter.Unresolved = true
	remaining, _ := f.app.StateFor(s).Comments.List(filter)
	if len(remaining) != 1 {
		t.Error("comments were resolved even though the review never posted")
	}
}

func TestOptionsOverrideDefaults(t *testing.T) {
	// The default providers must be replaceable without touching app code.
	repo := gittest.New(t)
	runner := exec.NewFakeRunner().
		Respond("rev-parse --show-toplevel", repo.Dir+"\n").
		Respond("rev-parse --absolute-git-dir", filepath.Join(repo.Dir, ".git")+"\n")

	a, err := app.Open(context.Background(), repo.Dir,
		app.WithRunner(runner),
		app.WithAIRegistry(ai.NewRegistry(&fakeAI{name: "custom", available: true})),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := a.AI.Names(); len(got) != 1 || got[0] != "custom" {
		t.Errorf("AI providers = %v, want the injected one", got)
	}
}

// openFixture is an App whose git config the test writes, and whose opener is
// recorded rather than run.
func openFixture(t *testing.T, config map[string]string) (*app.App, *exec.FakeRunner) {
	t.Helper()
	repo := gittest.New(t)

	var records []string
	for key, value := range config {
		records = append(records, key+"\n"+value+"\x00")
	}
	slices.Sort(records)

	runner := exec.NewFakeRunner().
		Respond("rev-parse --show-toplevel", repo.Dir+"\n").
		Respond("rev-parse --absolute-git-dir", filepath.Join(repo.Dir, ".git")+"\n").
		Respond("get-regexp", strings.Join(records, ""))

	a, err := app.Open(context.Background(), repo.Dir, app.WithRunner(runner))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return a, runner
}

// opened returns the command line the last call ran, for comparing against the
// one the settings should have produced.
func opened(runner *exec.FakeRunner) []string {
	calls := runner.Calls()
	last := calls[len(calls)-1].Cmd
	return append([]string{last.Name}, last.Args...)
}

// OpenFile hands the opener an absolute path, since the diff's paths are
// relative to the root and peel may have been started anywhere below it.
func TestOpenFileHandsAnAbsolutePathToTheOpener(t *testing.T) {
	a, runner := openFixture(t, nil)
	runner.Respond(app.OpenCommand, "")

	if err := a.OpenFile(context.Background(), filepath.Join("cmd", "main.go")); err != nil {
		t.Fatalf("OpenFile: %v", err)
	}

	want := []string{app.OpenCommand, filepath.Join(a.Root, "cmd", "main.go")}
	if got := opened(runner); !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
}

// Which command opens a file is a git config setting, and the one for the file's
// extension beats the one for everything else.
func TestOpenFileRunsTheCommandConfiguredForTheExtension(t *testing.T) {
	config := map[string]string{
		app.OpenKey:          "zed",
		app.OpenKey + ".md":  "open -a Marked",
		app.OpenKey + ".png": "qlmanage -p",
	}

	for _, tc := range []struct {
		path string
		want []string
	}{
		{"internal/app/app.go", []string{"zed"}},
		{"README.md", []string{"open", "-a", "Marked"}},
		{"docs/screenshots/review.png", []string{"qlmanage", "-p"}},
		{"Makefile", []string{"zed"}},
		{"skills/.gitkeep", []string{"zed"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			a, runner := openFixture(t, config)
			runner.Respond("zed", "").Respond("open", "").Respond("qlmanage", "")

			if err := a.OpenFile(context.Background(), tc.path); err != nil {
				t.Fatalf("OpenFile: %v", err)
			}

			want := append(slices.Clone(tc.want), filepath.Join(a.Root, tc.path))
			if got := opened(runner); !slices.Equal(got, want) {
				t.Errorf("opening %s ran %v, want %v", tc.path, got, want)
			}
		})
	}
}

// An extension with no setting of its own falls back to the desktop opener when
// nothing names a command for every file either.
func TestOpenFileFallsBackToTheDesktopOpenerPerExtension(t *testing.T) {
	a, runner := openFixture(t, map[string]string{app.OpenKey + ".md": "zed"})
	runner.Respond(app.OpenCommand, "").Respond("zed", "")

	if err := a.OpenFile(context.Background(), "main.go"); err != nil {
		t.Fatalf("OpenFile: %v", err)
	}

	want := []string{app.OpenCommand, filepath.Join(a.Root, "main.go")}
	if got := opened(runner); !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
}

// Extensions are matched however they are written, since git lower-cases the
// key it stores them under.
func TestOpenFileMatchesTheExtensionWhateverItsCase(t *testing.T) {
	a, runner := openFixture(t, map[string]string{app.OpenKey + ".md": "zed"})
	runner.Respond("zed", "")

	if err := a.OpenFile(context.Background(), "README.MD"); err != nil {
		t.Fatalf("OpenFile: %v", err)
	}

	want := []string{"zed", filepath.Join(a.Root, "README.MD")}
	if got := opened(runner); !slices.Equal(got, want) {
		t.Errorf("ran %v, want %v", got, want)
	}
}

// The whole path, against real git and a real command: the setting is read from
// the repository's config, split into a command and its arguments, and handed
// the file last.
func TestOpenFileRunsTheCommandGitConfigNames(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	record := filepath.Join(dir, "opened")
	opener := filepath.Join(dir, "opener.sh")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + record + "\n"
	if err := os.WriteFile(opener, []byte(script), 0o755); err != nil {
		t.Fatalf("write opener: %v", err)
	}
	f.repo.Git("config", app.OpenKey+".md", opener+" --wait")

	if err := f.app.OpenFile(f.ctx, "notes.md"); err != nil {
		t.Fatalf("OpenFile: %v", err)
	}

	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatalf("read what the opener saw: %v", err)
	}
	want := "--wait\n" + filepath.Join(f.app.Root, "notes.md") + "\n"
	if string(got) != want {
		t.Errorf("the opener saw %q, want %q", got, want)
	}
}

func TestOpenFileReportsTheFailure(t *testing.T) {
	a, runner := openFixture(t, nil)
	runner.RespondErr(app.OpenCommand, "no application knows how to open it", 1)

	if err := a.OpenFile(context.Background(), "main.go"); err == nil {
		t.Fatal("OpenFile succeeded despite the opener failing")
	}
}

// Copy pipes the text into the first clipboard tool this machine has, so peel
// reaches the clipboard on a desktop, under Wayland, under X11 and under WSL
// without being told which it is on.
func TestCopyPipesTheTextIntoTheFirstClipboardToolOnPath(t *testing.T) {
	for _, want := range app.ClipboardCommands {
		t.Run(want[0], func(t *testing.T) {
			repo := gittest.New(t)
			runner := exec.NewFakeRunner().
				Respond("rev-parse --show-toplevel", repo.Dir+"\n").
				Respond("rev-parse --absolute-git-dir", filepath.Join(repo.Dir, ".git")+"\n").
				Respond(strings.Join(want, " "), "")

			a, err := app.Open(context.Background(), repo.Dir,
				app.WithRunner(runner),
				app.WithLookPath(func(name string) bool { return name == want[0] }),
			)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if err := a.Copy(context.Background(), "the review"); err != nil {
				t.Fatalf("Copy: %v", err)
			}

			calls := runner.Calls()
			last := calls[len(calls)-1]
			if got := append([]string{last.Cmd.Name}, last.Cmd.Args...); !slices.Equal(got, want) {
				t.Errorf("ran %v, want %v", got, want)
			}
			if last.Stdin != "the review" {
				t.Errorf("stdin = %q, want the text to copy", last.Stdin)
			}
		})
	}
}

// A machine with no clipboard tool says so, and names the ones peel would use,
// rather than reporting a copy that never happened.
func TestCopyWithoutAClipboardToolNamesTheOnesItWanted(t *testing.T) {
	repo := gittest.New(t)
	runner := exec.NewFakeRunner().
		Respond("rev-parse --show-toplevel", repo.Dir+"\n").
		Respond("rev-parse --absolute-git-dir", filepath.Join(repo.Dir, ".git")+"\n")

	a, err := app.Open(context.Background(), repo.Dir,
		app.WithRunner(runner),
		app.WithLookPath(func(string) bool { return false }),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	err = a.Copy(context.Background(), "the review")
	if err == nil {
		t.Fatal("Copy succeeded with nothing to copy with")
	}
	if !strings.Contains(err.Error(), app.ClipboardCommands[0][0]) {
		t.Errorf("error = %q, want it to name a tool to install", err)
	}
}

// mustAddComment stores a note in whichever review it belongs to: the
// repository's own, or the file named after the pull request it is about.
func mustAddComment(t *testing.T, f *fixture, c store.Comment) store.Comment {
	t.Helper()
	got, err := f.app.StateForTarget(c.Target).Comments.Add(c)
	if err != nil {
		t.Fatalf("Add(%+v): %v", c, err)
	}
	return got
}
