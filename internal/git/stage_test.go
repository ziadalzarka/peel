package git_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/exec"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/gittest"
)

// harness wires a temporary repo to a Repo and Stager under test.
type harness struct {
	t       *testing.T
	fixture *gittest.Repo
	repo    *git.Repo
	stager  *git.Stager
	ctx     context.Context
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fixture := gittest.New(t)
	repo := git.NewRepo(fixture.Dir, exec.NewOSRunner())
	return &harness{
		t:       t,
		fixture: fixture,
		repo:    repo,
		stager:  git.NewStager(repo),
		ctx:     context.Background(),
	}
}

func (h *harness) status() git.Status {
	h.t.Helper()
	s, err := h.repo.LoadStatus(h.ctx)
	if err != nil {
		h.t.Fatalf("LoadStatus: %v", err)
	}
	return s
}

// entry returns the status entry for path, failing if it is absent.
func (h *harness) entry(path string) git.FileEntry {
	h.t.Helper()
	e, ok := h.status().Entry(path)
	if !ok {
		h.t.Fatalf("no status entry for %s", path)
	}
	return e
}

// numbered builds a file of n lines: "line1\nline2\n...".
func numbered(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString("line")
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
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

// --- Whole-file staging ---

// A file is the unit: every hunk in it goes to the index together, and nothing
// is left behind for a second keypress.
func TestStageFileTakesEveryHunkInIt(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("f.txt", numbered(30))
	h.fixture.Commit("base")

	// Two well-separated edits produce two hunks.
	content := strings.Replace(numbered(30), "line3\n", "line3-EDITED\n", 1)
	content = strings.Replace(content, "line25\n", "line25-EDITED\n", 1)
	h.fixture.Write("f.txt", content)

	if got := len(h.entry("f.txt").Unstaged.Hunks); got != 2 {
		t.Fatalf("got %d hunks, want 2", got)
	}

	if err := h.stager.StageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}

	if got := h.fixture.Staged("f.txt"); got != strings.TrimRight(content, "\n") {
		t.Errorf("index contents wrong:\ngot:\n%s\nwant:\n%s", got, content)
	}
	if got := h.entry("f.txt").State(); got != git.StateStaged {
		t.Errorf("State() = %v, want staged", got)
	}
}

func TestStageFileLeavesOtherFilesUnstaged(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", "a\n")
	h.fixture.Write("b.txt", "b\n")
	h.fixture.Commit("base")
	h.fixture.Write("a.txt", "a-EDITED\n")
	h.fixture.Write("b.txt", "b-EDITED\n")

	if err := h.stager.StageFile(h.ctx, "a.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}

	if got := h.entry("a.txt").State(); got != git.StateStaged {
		t.Errorf("a.txt State() = %v, want staged", got)
	}
	if got := h.entry("b.txt").State(); got != git.StateUnstaged {
		t.Errorf("b.txt State() = %v, want unstaged", got)
	}
}

func TestStageAndUnstageWholeFile(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("f.txt", numbered(10))
	h.fixture.Commit("base")
	h.fixture.Write("f.txt", strings.Replace(numbered(10), "line4\n", "line4-EDITED\n", 1))
	before := h.fixture.Read("f.txt")

	if err := h.stager.StageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if got := h.entry("f.txt").State(); got != git.StateStaged {
		t.Errorf("State() = %v, want staged", got)
	}

	if err := h.stager.UnstageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("UnstageFile: %v", err)
	}
	if got := h.entry("f.txt").State(); got != git.StateUnstaged {
		t.Errorf("State() = %v, want unstaged", got)
	}
	if got := h.fixture.Read("f.txt"); got != before {
		t.Error("the round trip changed the working tree")
	}
	if got := h.fixture.Staged("f.txt"); got != strings.TrimRight(numbered(10), "\n") {
		t.Errorf("index not restored to HEAD:\n%s", got)
	}
}

func TestStageAllAndUnstageAll(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", "1\n")
	h.fixture.Commit("base")
	h.fixture.Write("a.txt", "2\n")
	h.fixture.Write("b.txt", "new\n")

	if err := h.stager.StageAll(h.ctx); err != nil {
		t.Fatalf("StageAll: %v", err)
	}
	for _, path := range []string{"a.txt", "b.txt"} {
		if got := h.entry(path).State(); got != git.StateStaged {
			t.Errorf("%s State() = %v, want staged", path, got)
		}
	}

	if err := h.stager.UnstageAll(h.ctx); err != nil {
		t.Fatalf("UnstageAll: %v", err)
	}
	for _, f := range h.status().Files {
		if f.State() == git.StateStaged {
			t.Errorf("%s still staged after UnstageAll", f.Path)
		}
	}
}

// --- Test corpus from the spec: five shapes that must all behave. ---

func TestCorpusRename(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("from.txt", numbered(20))
	h.fixture.Commit("base")

	h.fixture.Git("mv", "from.txt", "to.txt")
	h.fixture.Git("restore", "--staged", ".")

	// --no-renames makes this a delete plus an add, which is the diff there is
	// to review: two files, each staged on its own.
	status := h.status()
	from, okFrom := status.Entry("from.txt")
	to, okTo := status.Entry("to.txt")
	if !okFrom || !okTo {
		t.Fatalf("want both sides of the rename, got %v", pathsOf(status))
	}
	if from.Unstaged == nil || from.Unstaged.Status != git.StatusDeleted {
		t.Errorf("from.txt not reported as a deletion")
	}
	if !to.Untracked {
		t.Errorf("to.txt not reported as untracked")
	}

	// Staging both halves reproduces the rename in the index.
	if err := h.stager.StageFile(h.ctx, "from.txt"); err != nil {
		t.Fatalf("StageFile(from.txt): %v", err)
	}
	if err := h.stager.StageFile(h.ctx, "to.txt"); err != nil {
		t.Fatalf("StageFile(to.txt): %v", err)
	}

	if got := h.fixture.Staged("to.txt"); got != strings.TrimRight(numbered(20), "\n") {
		t.Errorf("renamed file content wrong in index:\n%s", got)
	}
	if _, err := h.fixture.TryGit("show", ":from.txt"); err == nil {
		t.Error("from.txt still present in the index after staging the rename")
	}
}

func TestCorpusBinaryFile(t *testing.T) {
	h := newHarness(t)
	binary := []byte{0x00, 0x01, 0x02, 0xff, 0xfe, 0x00, 0x42}
	h.fixture.WriteBytes("img.bin", binary)
	h.fixture.Commit("base")

	h.fixture.WriteBytes("img.bin", append(binary, 0x99, 0x00, 0x11))

	e := h.entry("img.bin")
	if !e.IsBinary() {
		t.Fatal("binary file not detected as binary")
	}

	// A binary file has no diff to show, but staging it is the same operation as
	// staging any other file.
	if err := h.stager.StageFile(h.ctx, "img.bin"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if got := h.entry("img.bin").State(); got != git.StateStaged {
		t.Errorf("State() = %v, want staged", got)
	}
}

func TestCorpusUntrackedFile(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("existing.txt", "x\n")
	h.fixture.Commit("base")

	h.fixture.Write("new.txt", "alpha\nbeta\ngamma\n")

	e := h.entry("new.txt")
	if !e.Untracked {
		t.Fatal("new.txt not marked untracked")
	}
	// Its contents must be reviewable before it is staged, without the index
	// having been touched.
	if e.Unstaged == nil {
		t.Fatal("untracked file has no synthesized diff")
	}
	if e.Unstaged.Status != git.StatusAdded {
		t.Errorf("Status = %q, want added", e.Unstaged.Status)
	}
	added, removed := e.Unstaged.Stats()
	if added != 3 || removed != 0 {
		t.Errorf("Stats() = +%d -%d, want +3 -0", added, removed)
	}
	if _, err := h.fixture.TryGit("show", ":new.txt"); err == nil {
		t.Error("reviewing an untracked file added it to the index")
	}

	if err := h.stager.StageFile(h.ctx, "new.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if got := h.fixture.Staged("new.txt"); got != "alpha\nbeta\ngamma" {
		t.Errorf("index contents = %q", got)
	}
}

func TestCorpusNoTrailingNewline(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("f.txt", "alpha\nbeta")
	h.fixture.Commit("base")

	h.fixture.Write("f.txt", "alpha\nBETA")

	if err := h.stager.StageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}

	// The final byte is the whole point: a lost marker appends a newline.
	if got := h.fixture.StagedRaw("f.txt"); got != "alpha\nBETA" {
		t.Errorf("index contents = %q, want %q", got, "alpha\nBETA")
	}
}

func TestCorpusAddTrailingNewline(t *testing.T) {
	// The reverse case: a file that gains a trailing newline.
	h := newHarness(t)
	h.fixture.Write("f.txt", "alpha\nbeta")
	h.fixture.Commit("base")
	h.fixture.Write("f.txt", "alpha\nbeta\n")

	if err := h.stager.StageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if got := h.fixture.StagedRaw("f.txt"); got != "alpha\nbeta\n" {
		t.Errorf("index contents = %q, want %q", got, "alpha\nbeta\n")
	}
}

func TestCorpusPartiallyStagedAndPartiallyModified(t *testing.T) {
	// One file with changes in the index and different changes in the working
	// tree at the same time. peel cannot create this state any more, but git can,
	// and it still has to be reviewable and stageable.
	h := newHarness(t)
	h.fixture.Write("f.txt", numbered(40))
	h.fixture.Commit("base")

	h.fixture.Write("f.txt", strings.Replace(numbered(40), "line5\n", "line5-STAGED\n", 1))
	h.fixture.Git("add", "f.txt")

	both := strings.Replace(numbered(40), "line5\n", "line5-STAGED\n", 1)
	both = strings.Replace(both, "line30\n", "line30-WORKTREE\n", 1)
	h.fixture.Write("f.txt", both)

	e := h.entry("f.txt")
	if e.State() != git.StatePartial {
		t.Fatalf("State() = %v, want partial", e.State())
	}
	if e.Staged == nil || e.Unstaged == nil {
		t.Fatal("want both a staged and an unstaged diff")
	}
	if len(e.Staged.Hunks) != 1 || len(e.Unstaged.Hunks) != 1 {
		t.Fatalf("got %d staged / %d unstaged hunks, want 1 each",
			len(e.Staged.Hunks), len(e.Unstaged.Hunks))
	}

	// Staging the file takes the working-tree half without disturbing the half
	// already in the index.
	if err := h.stager.StageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if got := h.fixture.Staged("f.txt"); got != strings.TrimRight(both, "\n") {
		t.Errorf("index contents wrong:\n%s", got)
	}
	if got := h.entry("f.txt").State(); got != git.StateStaged {
		t.Errorf("State() = %v, want staged", got)
	}
}

func TestUnstageLeavesTheWorkingTreeAlone(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("f.txt", numbered(40))
	h.fixture.Commit("base")

	h.fixture.Write("f.txt", strings.Replace(numbered(40), "line5\n", "line5-STAGED\n", 1))
	h.fixture.Git("add", "f.txt")
	both := strings.Replace(numbered(40), "line5\n", "line5-STAGED\n", 1)
	both = strings.Replace(both, "line30\n", "line30-WORKTREE\n", 1)
	h.fixture.Write("f.txt", both)

	if err := h.stager.UnstageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("UnstageFile: %v", err)
	}

	if got := h.fixture.Staged("f.txt"); got != strings.TrimRight(numbered(40), "\n") {
		t.Errorf("index not returned to HEAD:\n%s", got)
	}
	if got := h.fixture.Read("f.txt"); got != both {
		t.Error("unstaging modified the working tree")
	}
}

// --- Deletions, CRLF, status ---

func TestStageDeletedFile(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("gone.txt", "a\nb\n")
	h.fixture.Write("kept.txt", "x\n")
	h.fixture.Commit("base")
	h.fixture.Remove("gone.txt")

	e := h.entry("gone.txt")
	if e.Unstaged == nil || e.Unstaged.Status != git.StatusDeleted {
		t.Fatalf("gone.txt not reported as deleted")
	}

	if err := h.stager.StageFile(h.ctx, "gone.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if _, err := h.fixture.TryGit("show", ":gone.txt"); err == nil {
		t.Error("deleted file still present in the index")
	}
}

func TestCRLFLineEndingsSurviveStaging(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("f.txt", "alpha\r\nbeta\r\n")
	h.fixture.Commit("base")
	h.fixture.Write("f.txt", "alpha\r\nBETA\r\n")

	if err := h.stager.StageFile(h.ctx, "f.txt"); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if got := h.fixture.StagedRaw("f.txt"); got != "alpha\r\nBETA\r\n" {
		t.Errorf("index contents = %q, want CRLF preserved", got)
	}
}

func TestLoadStatusOnCleanTree(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("f.txt", "a\n")
	h.fixture.Commit("base")

	if s := h.status(); !s.IsEmpty() {
		t.Errorf("clean tree reported %v", pathsOf(s))
	}
}

func TestLoadStatusWithUnbornHead(t *testing.T) {
	// A repository with no commits must still be reviewable.
	h := newHarness(t)
	h.fixture.Write("f.txt", "a\n")

	s := h.status()
	e, ok := s.Entry("f.txt")
	if !ok {
		t.Fatalf("f.txt missing from status: %v", pathsOf(s))
	}
	if !e.Untracked {
		t.Error("f.txt not marked untracked")
	}

	h.fixture.Git("add", "f.txt")
	e = h.entry("f.txt")
	if e.State() != git.StateStaged {
		t.Errorf("State() = %v, want staged", e.State())
	}
}

func TestGitDirAndRoot(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("f.txt", "a\n")
	h.fixture.Commit("base")

	root, err := h.repo.Root(h.ctx)
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if root == "" {
		t.Error("Root() empty")
	}

	gitDir, err := h.repo.GitDir(h.ctx)
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	if !strings.HasSuffix(gitDir, ".git") {
		t.Errorf("GitDir() = %q, want it to end in .git", gitDir)
	}
}

func TestRootFailsOutsideRepository(t *testing.T) {
	dir := t.TempDir()
	repo := git.NewRepo(dir, exec.NewOSRunner())
	if _, err := repo.Root(context.Background()); err == nil {
		t.Fatal("Root succeeded outside a repository, want error")
	}
}

// ConfigSection is how peel's own settings are read, so it has to hand back the
// section-wide key and the per-extension ones under it, values with spaces
// included.
func TestConfigSectionReadsEverySettingUnderIt(t *testing.T) {
	h := newHarness(t)
	h.fixture.Git("config", "peel.open", "zed")
	h.fixture.Git("config", "peel.open.md", "open -a Marked")
	h.fixture.Git("config", "user.name", "not peel")

	got, err := h.repo.ConfigSection(h.ctx, "peel")
	if err != nil {
		t.Fatalf("ConfigSection: %v", err)
	}

	want := map[string]string{"peel.open": "zed", "peel.open.md": "open -a Marked"}
	if len(got) != len(want) {
		t.Fatalf("ConfigSection = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %q, want %q", key, got[key], value)
		}
	}
}

// A section nobody has set is the normal case, not a failure.
func TestConfigSectionIsEmptyWhenNothingIsSet(t *testing.T) {
	h := newHarness(t)

	got, err := h.repo.ConfigSection(h.ctx, "peel")
	if err != nil {
		t.Fatalf("ConfigSection: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ConfigSection = %v, want it empty", got)
	}
}

func pathsOf(s git.Status) []string {
	out := make([]string, 0, len(s.Files))
	for _, f := range s.Files {
		out = append(out, f.Path)
	}
	return out
}
