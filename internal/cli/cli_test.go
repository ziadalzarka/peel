package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/cli"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/gittest"
)

// fakeAI returns a fixed narrative.
type fakeAI struct {
	body  string
	err   error
	calls int
}

func (f *fakeAI) Name() string                   { return "fake-ai" }
func (f *fakeAI) Description() string            { return "fake AI provider" }
func (f *fakeAI) Available(context.Context) bool { return true }

func (f *fakeAI) Walkthrough(context.Context, ai.Request) (string, error) {
	f.calls++
	return f.body, f.err
}

// fakeForge serves one pull request and records submitted reviews.
type fakeForge struct {
	diff      string
	submitted []forge.Review
	submitErr error
}

func (f *fakeForge) Name() string                   { return "fake-forge" }
func (f *fakeForge) Description() string            { return "fake forge" }
func (f *fakeForge) Available(context.Context) bool { return true }

func (f *fakeForge) Parse(_ context.Context, _, ref string) (forge.Ref, error) {
	if ref == "bad" {
		return forge.Ref{}, errors.New("cannot read \"bad\" as a pull request")
	}
	return forge.Ref{Owner: "o", Repo: "r", Number: 412}, nil
}

func (f *fakeForge) Fetch(context.Context, forge.Ref) (*forge.PullRequest, error) {
	return &forge.PullRequest{
		Ref:     forge.Ref{Owner: "o", Repo: "r", Number: 412},
		Title:   "Drop the document key",
		Author:  "ziad",
		BaseRef: "main",
		HeadRef: "feature/drop-key",
		URL:     "https://github.com/o/r/pull/412",
		State:   "OPEN",
		Diff:    f.diff,
	}, nil
}

func (f *fakeForge) SubmitReview(_ context.Context, _ forge.Ref, r forge.Review) error {
	if f.submitErr != nil {
		return f.submitErr
	}
	f.submitted = append(f.submitted, r)
	return nil
}

// harness runs CLI commands against a temporary repository.
type harness struct {
	t      *testing.T
	repo   *gittest.Repo
	cli    *cli.CLI
	stdout *bytes.Buffer
	stderr *bytes.Buffer
	stdin  *bytes.Buffer
	ai     *fakeAI
	forge  *fakeForge
	tuiRan bool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	repo := gittest.New(t)
	aiProvider := &fakeAI{body: "## What changed\n\nIt moves keys."}
	forgeProvider := &fakeForge{
		diff: "diff --git a/pr.go b/pr.go\n--- a/pr.go\n+++ b/pr.go\n@@ -1 +1 @@\n-old\n+new\n",
	}

	h := &harness{
		t:      t,
		repo:   repo,
		stdout: &bytes.Buffer{},
		stderr: &bytes.Buffer{},
		stdin:  &bytes.Buffer{},
		ai:     aiProvider,
		forge:  forgeProvider,
	}
	h.cli = &cli.CLI{
		Stdout: h.stdout,
		Stderr: h.stderr,
		Stdin:  h.stdin,
		Dir:    repo.Dir,
		OpenApp: func(ctx context.Context, dir string) (*app.App, error) {
			return app.Open(ctx, dir,
				app.WithAIRegistry(ai.NewRegistry(aiProvider)),
				app.WithForgeRegistry(forge.NewRegistry(forgeProvider)),
			)
		},
		RunTUI: func(context.Context, *app.App, *app.Session) error {
			h.tuiRan = true
			return nil
		},
	}
	return h
}

// run executes a command and returns its exit code, resetting output first.
func (h *harness) run(args ...string) int {
	h.t.Helper()
	h.stdout.Reset()
	h.stderr.Reset()
	return h.cli.Run(context.Background(), args)
}

// mustRun executes a command and fails the test on a non-zero exit.
func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	if code := h.run(args...); code != 0 {
		h.t.Fatalf("peel %s exited %d: %s", strings.Join(args, " "), code, h.stderr.String())
	}
	return h.stdout.String()
}

func (h *harness) out() string { return h.stdout.String() }
func (h *harness) err() string { return h.stderr.String() }

// dirty puts one committed file and one modification in the repository.
func (h *harness) dirty() {
	h.t.Helper()
	h.repo.Write("a.txt", "one\ntwo\nthree\n")
	h.repo.Commit("base")
	h.repo.Write("a.txt", "one\nCHANGED\nthree\n")
}

func TestVersion(t *testing.T) {
	h := newHarness(t)
	if got := h.mustRun("version"); strings.TrimSpace(got) == "" {
		t.Error("version printed nothing")
	}
	if got := h.mustRun("--version"); strings.TrimSpace(got) == "" {
		t.Error("--version printed nothing")
	}
}

func TestHelp(t *testing.T) {
	h := newHarness(t)
	got := h.mustRun("--help")

	for _, want := range []string{"comment", "hunks", "walkthrough", "pr", "--pr"} {
		if !strings.Contains(got, want) {
			t.Errorf("help missing %q", want)
		}
	}
	// The read-only stance should be discoverable, not a surprise.
	if !strings.Contains(got, "git add") {
		t.Error("help does not explain that staging is UI-only")
	}
}

func TestUnknownCommand(t *testing.T) {
	h := newHarness(t)
	if code := h.run("frobnicate"); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(h.err(), "unknown command") {
		t.Errorf("stderr = %q", h.err())
	}
}

func TestNoArgsLaunchesTUI(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	if code := h.run(); code != 0 {
		t.Fatalf("exit code = %d: %s", code, h.err())
	}
	if !h.tuiRan {
		t.Error("the TUI was not launched")
	}
}

func TestOutsideRepositoryFails(t *testing.T) {
	h := newHarness(t)
	h.cli.Dir = t.TempDir()
	h.cli.OpenApp = func(ctx context.Context, dir string) (*app.App, error) {
		return app.Open(ctx, dir)
	}

	if code := h.run("hunks", "list"); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(h.err(), "git repository") {
		t.Errorf("stderr = %q", h.err())
	}
}

// --- comment ---

func TestCommentAddAndList(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	out := h.mustRun("comment", "add", "--file", "a.txt", "--line", "2", "--body", "this leaks the tx")
	if !strings.Contains(out, "a.txt:2") {
		t.Errorf("add output = %q", out)
	}

	out = h.mustRun("comment", "list")
	if !strings.Contains(out, "this leaks the tx") {
		t.Errorf("list output = %q", out)
	}
}

func TestCommentAddReadsStdin(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.stdin.WriteString("body from stdin\n")

	h.mustRun("comment", "add", "--file", "a.txt", "--line", "1")

	out := h.mustRun("comment", "list", "--json")
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	if got[0]["body"] != "body from stdin" {
		t.Errorf("body = %v", got[0]["body"])
	}
}

func TestCommentAddDefaultsToAgentAuthor(t *testing.T) {
	// The CLI is what an agent drives, so its notes are attributed to the agent
	// unless it says otherwise.
	h := newHarness(t)
	h.dirty()
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "1", "--body", "x")

	var got []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json"), &got)
	if got[0]["author"] != "agent" {
		t.Errorf("author = %v, want agent", got[0]["author"])
	}
}

func TestCommentListJSONShape(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "2", "--body", "note", "--author", "user", "--hunk", "a.txt:@-1,3+1,3")

	var got []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json"), &got)
	if len(got) != 1 {
		t.Fatalf("got %d comments, want 1", len(got))
	}

	for _, key := range []string{"id", "file", "line", "side", "body", "author", "resolved", "createdAt", "hunk"} {
		if _, ok := got[0][key]; !ok {
			t.Errorf("JSON missing key %q: %v", key, got[0])
		}
	}
	if got[0]["line"] != float64(2) {
		t.Errorf("line = %v", got[0]["line"])
	}
	if got[0]["side"] != "new" {
		t.Errorf("side = %v, want new by default", got[0]["side"])
	}
}

func TestCommentListEmpty(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	if got := h.mustRun("comment", "list"); !strings.Contains(got, "no comments") {
		t.Errorf("output = %q", got)
	}

	var got []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json"), &got)
	if len(got) != 0 {
		t.Errorf("JSON = %v, want an empty array", got)
	}
}

func TestCommentListFilters(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.repo.Write("b.txt", "x\n")

	h.mustRun("comment", "add", "--file", "a.txt", "--line", "1", "--body", "on a")
	h.mustRun("comment", "add", "--file", "b.txt", "--line", "1", "--body", "on b")
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "2", "--body", "by user", "--author", "user")

	var byFile []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json", "--file", "a.txt"), &byFile)
	if len(byFile) != 2 {
		t.Errorf("--file a.txt returned %d comments, want 2", len(byFile))
	}

	var byAuthor []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json", "--author", "user"), &byAuthor)
	if len(byAuthor) != 1 {
		t.Errorf("--author user returned %d comments, want 1", len(byAuthor))
	}
}

func TestCommentListRejectsBadAuthor(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("comment", "list", "--author", "robot"); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestCommentResolveAndFilter(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "1", "--body", "first")
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "2", "--body", "second")

	var all []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json"), &all)
	id := all[0]["id"].(string)

	h.mustRun("comment", "resolve", id)

	var unresolved []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json", "--unresolved"), &unresolved)
	if len(unresolved) != 1 {
		t.Errorf("got %d unresolved comments, want 1", len(unresolved))
	}

	// Resolving is reversible.
	h.mustRun("comment", "resolve", "--undo", id)
	mustJSON(t, h.mustRun("comment", "list", "--json", "--unresolved"), &unresolved)
	if len(unresolved) != 2 {
		t.Errorf("got %d unresolved comments after undo, want 2", len(unresolved))
	}
}

func TestCommentRemove(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "1", "--body", "gone")

	var all []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json"), &all)
	id := all[0]["id"].(string)

	h.mustRun("comment", "rm", id)
	mustJSON(t, h.mustRun("comment", "list", "--json"), &all)
	if len(all) != 0 {
		t.Errorf("comment survived removal: %v", all)
	}
}

func TestCommentRemoveUnknownID(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("comment", "rm", "nope"); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(h.err(), "not found") {
		t.Errorf("stderr = %q", h.err())
	}
}

func TestCommentClear(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.repo.Write("b.txt", "x\n")
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "1", "--body", "one")
	h.mustRun("comment", "add", "--file", "b.txt", "--line", "1", "--body", "two")

	if got := h.mustRun("comment", "clear", "--file", "a.txt"); !strings.Contains(got, "1 comment") {
		t.Errorf("clear output = %q", got)
	}

	var left []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json"), &left)
	if len(left) != 1 || left[0]["file"] != "b.txt" {
		t.Errorf("remaining = %v", left)
	}
}

func TestCommentClearResolvedOnly(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "1", "--body", "done")
	h.mustRun("comment", "add", "--file", "a.txt", "--line", "2", "--body", "open")

	var all []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json"), &all)
	h.mustRun("comment", "resolve", all[0]["id"].(string))

	h.mustRun("comment", "clear", "--resolved")

	mustJSON(t, h.mustRun("comment", "list", "--json"), &all)
	if len(all) != 1 || all[0]["body"] != "open" {
		t.Errorf("remaining = %v, want only the unresolved comment", all)
	}
}

func TestCommentAddValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no file", []string{"comment", "add", "--body", "x"}},
		{"no body or stdin", []string{"comment", "add", "--file", "a.txt"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.dirty()
			if code := h.run(tt.args...); code != 2 {
				t.Errorf("exit code = %d, want 2 — %s", code, h.err())
			}
		})
	}
}

func TestCommentNeedsSubcommand(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("comment"); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(h.err(), "list") {
		t.Errorf("stderr = %q, want it to list the subcommands", h.err())
	}
}

// --- hunks ---

func TestHunksList(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	out := h.mustRun("hunks", "list")
	if !strings.Contains(out, "a.txt") {
		t.Errorf("output = %q", out)
	}
	if !strings.Contains(out, "unstaged") {
		t.Errorf("output = %q, want the staged state shown", out)
	}
	if !strings.Contains(out, ":@-") {
		t.Errorf("output = %q, want addressable hunk ids", out)
	}
}

func TestHunksListJSON(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	var got []map[string]any
	mustJSON(t, h.mustRun("hunks", "list", "--json"), &got)
	if len(got) != 1 {
		t.Fatalf("got %d hunks, want 1: %v", len(got), got)
	}
	for _, key := range []string{"id", "file", "staged", "header", "added", "removed"} {
		if _, ok := got[0][key]; !ok {
			t.Errorf("JSON missing key %q: %v", key, got[0])
		}
	}
	if got[0]["staged"] != false {
		t.Errorf("staged = %v, want false", got[0]["staged"])
	}
	// The id must round trip as a hunk address.
	if id, _ := got[0]["id"].(string); !strings.HasPrefix(id, "a.txt:@-") {
		t.Errorf("id = %q", id)
	}
}

func TestHunksListSeparatesStagedFromUnstaged(t *testing.T) {
	h := newHarness(t)
	h.repo.Write("a.txt", numbered(40))
	h.repo.Commit("base")
	h.repo.Write("a.txt", strings.Replace(numbered(40), "line5\n", "line5-STAGED\n", 1))
	h.repo.Git("add", "a.txt")

	both := strings.Replace(numbered(40), "line5\n", "line5-STAGED\n", 1)
	both = strings.Replace(both, "line30\n", "line30-WORKTREE\n", 1)
	h.repo.Write("a.txt", both)

	var got []map[string]any
	mustJSON(t, h.mustRun("hunks", "list", "--json"), &got)
	if len(got) != 2 {
		t.Fatalf("got %d hunks, want 2 (one per side): %v", len(got), got)
	}

	var staged, unstaged int
	for _, hunk := range got {
		if hunk["staged"] == true {
			staged++
		} else {
			unstaged++
		}
	}
	if staged != 1 || unstaged != 1 {
		t.Errorf("staged=%d unstaged=%d, want 1 each", staged, unstaged)
	}

	var onlyStaged []map[string]any
	mustJSON(t, h.mustRun("hunks", "list", "--json", "--staged"), &onlyStaged)
	if len(onlyStaged) != 1 || onlyStaged[0]["staged"] != true {
		t.Errorf("--staged returned %v", onlyStaged)
	}
}

func TestHunksListMutuallyExclusiveFlags(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("hunks", "list", "--staged", "--unstaged"); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestHunksListBinaryFile(t *testing.T) {
	h := newHarness(t)
	h.repo.WriteBytes("img.bin", []byte{0, 1, 2, 0xff})
	h.repo.Commit("base")
	h.repo.WriteBytes("img.bin", []byte{0, 1, 2, 0xff, 0x99})

	var got []map[string]any
	mustJSON(t, h.mustRun("hunks", "list", "--json"), &got)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %v", len(got), got)
	}
	if got[0]["binary"] != true {
		t.Errorf("binary = %v, want true", got[0]["binary"])
	}
}

func TestHunksListCleanTree(t *testing.T) {
	h := newHarness(t)
	h.repo.Write("a.txt", "x\n")
	h.repo.Commit("base")

	if got := h.mustRun("hunks", "list"); !strings.Contains(got, "no changes") {
		t.Errorf("output = %q", got)
	}
}

func TestHunksStagingIsRefused(t *testing.T) {
	// The read-only stance must produce a clear message, not "unknown command".
	h := newHarness(t)
	h.dirty()

	for _, sub := range []string{"add", "rm", "stage", "unstage"} {
		t.Run(sub, func(t *testing.T) {
			if code := h.run("hunks", sub, "a.txt:@-1,3+1,3"); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(h.err(), "git add") {
				t.Errorf("stderr = %q, want it to point at git add", h.err())
			}
		})
	}
}

// --- walkthrough ---

func TestWalkthrough(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	out := h.mustRun("walkthrough")
	if !strings.Contains(out, "It moves keys.") {
		t.Errorf("output = %q", out)
	}

	// A second run is served from the cache.
	h.mustRun("walkthrough")
	if h.ai.calls != 1 {
		t.Errorf("provider called %d times, want 1", h.ai.calls)
	}

	h.mustRun("walkthrough", "--regen")
	if h.ai.calls != 2 {
		t.Errorf("provider called %d times after --regen, want 2", h.ai.calls)
	}
}

func TestWalkthroughCleanTree(t *testing.T) {
	h := newHarness(t)
	h.repo.Write("a.txt", "x\n")
	h.repo.Commit("base")

	if code := h.run("walkthrough"); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(h.err(), "no changes") {
		t.Errorf("stderr = %q", h.err())
	}
}

func TestWalkthroughProviderFailure(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.ai.err = errors.New("not authenticated")

	if code := h.run("walkthrough"); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(h.err(), "not authenticated") {
		t.Errorf("stderr = %q", h.err())
	}
}

func TestWalkthroughUnknownProvider(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("walkthrough", "--provider", "gpt"); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(h.err(), "fake-ai") {
		t.Errorf("stderr = %q, want it to list known providers", h.err())
	}
}

// --- providers ---

func TestProviders(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	out := h.mustRun("providers")
	for _, want := range []string{"fake-ai", "fake-forge", "available"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// --- pr ---

func TestPRView(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	out := h.mustRun("pr", "view", "412")
	for _, want := range []string{"#412", "Drop the document key", "feature/drop-key", "main"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPRViewJSON(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	var got map[string]any
	mustJSON(t, h.mustRun("pr", "view", "412", "--json"), &got)
	if got["number"] != float64(412) {
		t.Errorf("number = %v", got["number"])
	}
	if got["repo"] != "o/r" {
		t.Errorf("repo = %v", got["repo"])
	}
}

func TestPRViewViaGlobalFlag(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if got := h.mustRun("--pr", "412", "pr", "view"); !strings.Contains(got, "#412") {
		t.Errorf("output = %q", got)
	}
}

func TestPRViewNeedsARef(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("pr", "view"); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestPRSubmitRequiresConfirmation(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "this leaks")

	// No answer on stdin means declined — posting must not be the default.
	out := h.mustRun("pr", "submit", "412")
	if len(h.forge.submitted) != 0 {
		t.Error("a review was posted without confirmation")
	}
	if !strings.Contains(out, "cancelled") {
		t.Errorf("output = %q", out)
	}
}

func TestPRSubmitDeclined(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "note")
	h.stdin.WriteString("n\n")

	h.mustRun("pr", "submit", "412")
	if len(h.forge.submitted) != 0 {
		t.Error("a review was posted after declining")
	}
}

func TestPRSubmitConfirmed(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "this leaks")
	h.stdin.WriteString("y\n")

	out := h.mustRun("pr", "submit", "412")
	if len(h.forge.submitted) != 1 {
		t.Fatalf("submitted %d reviews, want 1", len(h.forge.submitted))
	}
	if got := h.forge.submitted[0].Comments; len(got) != 1 || got[0].Body != "this leaks" {
		t.Errorf("submitted comments = %v", got)
	}
	if !strings.Contains(out, "posted") {
		t.Errorf("output = %q", out)
	}
}

func TestPRSubmitYesSkipsPrompt(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "note")

	h.mustRun("pr", "submit", "412", "--yes")
	if len(h.forge.submitted) != 1 {
		t.Errorf("submitted %d reviews, want 1", len(h.forge.submitted))
	}
}

func TestPRSubmitPreviewsBeforeAsking(t *testing.T) {
	// The confirmation is only meaningful if the content is shown first.
	h := newHarness(t)
	h.dirty()
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "this leaks the tx")

	out := h.mustRun("pr", "submit", "412", "--dry-run")
	for _, want := range []string{"pr.go:1", "this leaks the tx", "dry run"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if len(h.forge.submitted) != 0 {
		t.Error("--dry-run posted a review")
	}
}

func TestPRSubmitEvents(t *testing.T) {
	tests := []struct {
		flag string
		want forge.ReviewEvent
	}{
		{"comment", forge.EventComment},
		{"approve", forge.EventApprove},
		{"request-changes", forge.EventRequestChanges},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			h := newHarness(t)
			h.dirty()
			h.mustRun("pr", "submit", "412", "--yes", "--body", "summary", "--event", tt.flag)
			if got := h.forge.submitted[0].Event; got != tt.want {
				t.Errorf("Event = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPRSubmitUnknownEvent(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("pr", "submit", "412", "--event", "merge", "--yes"); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

func TestPRSubmitResolvesLocally(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "note")

	h.mustRun("pr", "submit", "412", "--yes")

	var unresolved []map[string]any
	mustJSON(t, h.mustRun("--pr", "412", "comment", "list", "--json", "--unresolved"), &unresolved)
	if len(unresolved) != 0 {
		t.Errorf("%d comments still unresolved after submission", len(unresolved))
	}
}

func TestPRSubmitKeepUnresolved(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "note")

	h.mustRun("pr", "submit", "412", "--yes", "--keep-unresolved")

	var unresolved []map[string]any
	mustJSON(t, h.mustRun("--pr", "412", "comment", "list", "--json", "--unresolved"), &unresolved)
	if len(unresolved) != 1 {
		t.Errorf("got %d unresolved comments, want 1", len(unresolved))
	}
}

func TestPRSubmitEmptyReview(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("pr", "submit", "412", "--yes"); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if len(h.forge.submitted) != 0 {
		t.Error("an empty review was posted")
	}
}

func TestPRSubmitForgeFailure(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	h.forge.submitErr = errors.New("HTTP 422: line must be part of the diff")
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "note")

	if code := h.run("pr", "submit", "412", "--yes"); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(h.err(), "422") {
		t.Errorf("stderr = %q", h.err())
	}
}

func TestPRModeScopesCommentsSeparately(t *testing.T) {
	h := newHarness(t)
	h.dirty()

	h.mustRun("comment", "add", "--file", "a.txt", "--line", "1", "--body", "working tree note")
	h.mustRun("--pr", "412", "comment", "add", "--file", "pr.go", "--line", "1", "--body", "pr note")

	var working []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json"), &working)
	if len(working) != 1 || working[0]["body"] != "working tree note" {
		t.Errorf("working tree comments = %v", working)
	}

	var pr []map[string]any
	mustJSON(t, h.mustRun("--pr", "412", "comment", "list", "--json"), &pr)
	if len(pr) != 1 || pr[0]["body"] != "pr note" {
		t.Errorf("PR comments = %v", pr)
	}

	// --all crosses the boundary deliberately.
	var all []map[string]any
	mustJSON(t, h.mustRun("comment", "list", "--json", "--all"), &all)
	if len(all) != 2 {
		t.Errorf("--all returned %d comments, want 2", len(all))
	}
}

func TestPRLoadFailurePropagates(t *testing.T) {
	h := newHarness(t)
	h.dirty()
	if code := h.run("--pr", "bad", "hunks", "list"); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(h.err(), "bad") {
		t.Errorf("stderr = %q", h.err())
	}
}

func mustJSON(t *testing.T, in string, out any) {
	t.Helper()
	if err := json.Unmarshal([]byte(in), out); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, in)
	}
}

func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmtInt(&b, i)
	}
	return b.String()
}

func fmtInt(b *strings.Builder, i int) {
	b.WriteString("line")
	b.WriteString(itoa(i))
	b.WriteByte('\n')
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
