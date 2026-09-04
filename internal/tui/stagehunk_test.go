package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/gittest"
)

// twoHunkFile is one file changed in two places — the file the hunk mode exists
// for, where one change is finished and the other is still being thought about.
const twoHunkFile = `diff --git a/list.txt b/list.txt
index 1111111..2222222 100644
--- a/list.txt
+++ b/list.txt
@@ -1,5 +1,5 @@
-one
+ONE
 two
 three
 four
 five
@@ -8,4 +8,4 @@
 eight
 nine
-ten
+TEN
 eleven
`

// threeHunkFile is one file changed in three places, so a press can take a hunk
// and still leave more than one behind — which two hunks cannot, since staging
// one of two is staging the file.
const threeHunkFile = twoHunkFile + `@@ -20,3 +20,3 @@
 twenty
-twentyone
+TWENTYONE
 twentytwo
`

// twoHunkFiles is two files changed in more than one place each, for the presses
// that finish with one file and carry on into the next.
const twoHunkFiles = threeHunkFile + `diff --git a/notes.md b/notes.md
index 3333333..4444444 100644
--- a/notes.md
+++ b/notes.md
@@ -1,4 +1,4 @@
 alpha
-beta
+BETA
 gamma
 delta
@@ -10,3 +10,3 @@
 kappa
-lambda
+LAMBDA
 mu
`

// hunkModel is a model in the mode these tests are about: `s` on the hunk, which
// is where `S` puts a review that opened on the file.
func hunkModel(t *testing.T, backend *fakeBackend, opts ...Option) *Model {
	t.Helper()
	return newModel(t, backend, append(opts, WithStageMode(app.StageModeHunk))...)
}

// atHunk puts the cursor on the header of the nth hunk on screen, which is where
// a reviewer deciding about a hunk has just read down to.
func atHunk(t *testing.T, m *Model, n int) {
	t.Helper()
	row := m.doc.RowOfHunk(n)
	if row < 0 {
		t.Fatalf("hunk %d is not on screen", n)
	}
	m.moveTo(row)
}

// frozen stops the clock the double press is measured on, so two presses in a
// test are as close together as two presses of a finger.
func frozen(m *Model) *time.Time {
	at := time.Now()
	m.now = func() time.Time { return at }
	return &at
}

// The whole point, against real git: one hunk of a file goes into the index and
// the rest of the file stays out of it, on disk, untouched.
func TestStagingAHunkWritesThatHunkAloneToTheIndex(t *testing.T) {
	const before = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\ntwelve\n"
	const after = "ONE\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\nTWELVE\n"

	repo := gittest.New(t)
	repo.Write("list.txt", before)
	repo.Commit("base")
	repo.Write("list.txt", after)

	m := realModel(t, repo, WithStageMode(app.StageModeHunk))
	atHunk(t, m, 1)
	press(t, m, "s")

	if got, want := repo.StagedRaw("list.txt"), strings.Replace(before, "twelve", "TWELVE", 1); got != want {
		t.Errorf("index contents = %q, want only the hunk the cursor was in", got)
	}
	if got := repo.Read("list.txt"); got != after {
		t.Errorf("working tree = %q, want it untouched at %q", got, after)
	}
	if got := m.doc.Files[0].Entry.State(); got != git.StatePartial {
		t.Errorf("State() = %v, want the file drawn as half staged", got)
	}
	// The pass carries on inside the file: it is still open, on the one change
	// that has not been decided about.
	if m.doc.Files[0].Collapsed {
		t.Error("the file folded away with work still out of the index")
	}
	var left []git.HunkID
	for _, h := range m.doc.Hunks {
		if !h.Staged {
			left = append(left, h.ID)
		}
	}
	if len(left) != 1 {
		t.Errorf("hunks still out of the index = %v, want the one that was not staged", left)
	}
}

// Every other change is on screen before git is asked, and this one is no
// different: what moving a hunk does to the two diffs is arithmetic, not a
// question for git.
func TestAStagedHunkIsOffTheScreenBeforeGitIsAsked(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := hunkModel(t, backend)

	atHunk(t, m, 0)
	staged := m.doc.Hunks[0].ID
	m.Update(keyMsg("s"))

	if len(backend.stagedHunks) != 0 || backend.reloads != 0 {
		t.Fatalf("git was asked on the keypress: hunks %v, reloads %d", backend.stagedHunks, backend.reloads)
	}
	for _, h := range m.doc.Hunks {
		if h.ID == staged && !h.Staged {
			t.Error("the staged hunk is still drawn as work out of the index")
		}
	}
	if got := m.doc.Files[0].Entry.State(); got != git.StatePartial {
		t.Errorf("State() = %v, want the file already reading as half staged", got)
	}
	if got, _ := m.doc.Files[0].Entry.Staged.Stats(); got != 1 {
		t.Errorf("the index side counts %d additions, want the one that just went in", got)
	}
}

// The hunk it was on has gone into the index, so the cursor carries on to the
// next one still out of it — the rule the file mode follows between files, inside
// one.
func TestTheCursorCarriesOnToTheNextHunkOutOfTheIndex(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := hunkModel(t, backend)

	atHunk(t, m, 0)
	next := m.doc.Hunks[1].ID
	m.Update(keyMsg("s"))

	ref, ok := m.doc.HunkTargetAt(m.cursor)
	if !ok {
		t.Fatalf("the cursor left the hunks entirely, at row %d", m.cursor)
	}
	if ref.ID != next {
		t.Errorf("the cursor is on %v, want the hunk still out of the index at %v", ref.ID, next)
	}
}

// The keypress names the hunk the cursor is in and nothing else: the file it
// belongs to is the other mode.
func TestStagingAHunkAsksGitForThatHunkOnly(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := hunkModel(t, backend)

	atHunk(t, m, 1)
	want := m.doc.Hunks[1].ID
	press(t, m, "s")

	if got := backend.stagedHunks; len(got) != 1 || got[0] != want {
		t.Fatalf("StageHunk calls = %v, want [%v]", got, want)
	}
	if len(backend.stagedFiles) != 0 {
		t.Errorf("the file was staged too: %v", backend.stagedFiles)
	}
	if !strings.Contains(m.status, "one hunk of list.txt") {
		t.Errorf("status = %q, want it to say what was staged", m.status)
	}
}

// A hunk is addressed from anywhere inside it, the way a file is: the cursor is
// usually on the line the decision was made about, not on the header.
func TestStagingAHunkFromOneOfItsLines(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := hunkModel(t, backend)

	m.moveTo(lineRowOf(t, m, 1, 0))
	want := m.doc.Hunks[1].ID
	press(t, m, "s")

	if got := backend.stagedHunks; len(got) != 1 || got[0] != want {
		t.Fatalf("StageHunk calls = %v, want [%v]", got, want)
	}
}

// A file whose work is one hunk is the same decision at either size, so it goes
// in as the file: `git add` rather than a patch, which is what a deletion, a mode
// change and a file with no newline at its end all need. The press ends the way
// the file mode ends — folded away, and the cursor on the next file with work
// still out of the index.
func TestAFileWhoseWorkIsOneHunkGoesInAsTheFile(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	staged := parseFiles(t, twoFileDiff)
	staged[0].Staged, staged[0].Unstaged = staged[0].Unstaged, nil
	backend.nextSession = sessionOf(staged)
	m := hunkModel(t, backend)

	atHunk(t, m, 0)
	press(t, m, "s")

	if len(backend.stagedHunks) != 0 {
		t.Fatalf("a patch was applied for a file's only hunk: %v", backend.stagedHunks)
	}
	if got := backend.stagedFiles; len(got) != 1 || got[0] != "alpha.go" {
		t.Fatalf("StageFile calls = %v, want the whole file", got)
	}
	if !m.doc.Files[0].Collapsed {
		t.Error("the file its only hunk finished did not fold away")
	}
	if got := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path; got != "beta.txt" {
		t.Errorf("the cursor is on %q, want it moved on to beta.txt", got)
	}
}

// Two presses that fast are one decision about the whole file, and they leave
// the same index the file mode would.
func TestPressingTheKeyTwiceStagesTheFile(t *testing.T) {
	backend := newFakeBackend(newSession(t, threeHunkFile))
	m := hunkModel(t, backend)
	frozen(m)

	atHunk(t, m, 0)
	press(t, m, "s", "s")

	if got := backend.stagedFiles; len(got) != 1 || got[0] != "list.txt" {
		t.Fatalf("StageFile calls = %v, want the second press to take the file", got)
	}
	if len(backend.stagedHunks) != 1 {
		t.Errorf("StageHunk calls = %v, want only the first press", backend.stagedHunks)
	}
	if !m.doc.Files[0].Collapsed {
		t.Error("the file did not fold away, so the press did not finish it")
	}
}

// Far enough apart and it is two decisions about two hunks — a pass down a file
// reading each one is not a double press, however fast the reviewer is.
func TestTwoPressesApartStageTwoHunks(t *testing.T) {
	backend := newFakeBackend(newSession(t, threeHunkFile))
	m := hunkModel(t, backend)
	at := frozen(m)

	atHunk(t, m, 0)
	press(t, m, "s")
	*at = at.Add(doubleWindow + time.Millisecond)
	press(t, m, "s")

	if len(backend.stagedFiles) != 0 {
		t.Fatalf("StageFile calls = %v, want neither press to take the file", backend.stagedFiles)
	}
	if len(backend.stagedHunks) != 2 {
		t.Errorf("StageHunk calls = %v, want one for each press", backend.stagedHunks)
	}
}

// The cursor moves on by itself once a file is finished, so a second press that
// fast can land in a file nobody has read yet — which is a first decision about
// that file, not a second about the last one.
func TestADoublePressDoesNotCarryIntoTheNextFile(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFiles))
	m := hunkModel(t, backend)
	frozen(m)

	atHunk(t, m, 0)
	press(t, m, "s")
	atHunk(t, m, 3)
	press(t, m, "s")

	if len(backend.stagedFiles) != 0 {
		t.Fatalf("StageFile calls = %v, want the press in the next file to take its hunk", backend.stagedFiles)
	}
	if got := backend.stagedHunks; len(got) != 2 {
		t.Errorf("StageHunk calls = %v, want one in each file", got)
	}
}

// A review opens on the file — the size a diff is dealt with in more often than
// not — and `S` moves `s` onto the hunk for the file that wants it.
func TestSwitchingTheModePutsTheHunkUnderTheSameKey(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := newModel(t, backend)

	atHunk(t, m, 1)
	want := m.doc.Hunks[1].ID
	press(t, m, "S", "s")

	if got := backend.stagedHunks; len(got) != 1 || got[0] != want {
		t.Fatalf("StageHunk calls = %v, want the hunk %v", got, want)
	}
	if len(backend.stagedFiles) != 0 {
		t.Errorf("the file went in too: %v", backend.stagedFiles)
	}
}

// It is a switch, not a way in: the mode it left is one press back.
func TestSwitchingTwiceIsBackWhereItStarted(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := newModel(t, backend)

	atHunk(t, m, 1)
	press(t, m, "S", "S", "s")

	if got := backend.stagedFiles; len(got) != 1 || got[0] != "list.txt" {
		t.Fatalf("StageFile calls = %v, want the whole file", got)
	}
	if len(backend.stagedHunks) != 0 {
		t.Errorf("a hunk went in on its own: %v", backend.stagedHunks)
	}
}

// A mode nobody can see is a mode that stages the wrong thing, so the switch says
// what it did and the footer and the help screen name what `s` takes now.
func TestTheModeIsWhatTheScreenSays(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := newModel(t, backend)

	if got := m.hints(); !strings.Contains(got, "s stage file") || !strings.Contains(got, "S stage by hunk") {
		t.Errorf("footer = %q, want it to name the mode the review opened in", got)
	}
	press(t, m, "S")
	if !strings.Contains(m.status, "hunk") {
		t.Errorf("status = %q, want the switch to say what s takes now", m.status)
	}
	if got := m.hints(); !strings.Contains(got, "s stage hunk") || !strings.Contains(got, "S stage by file") {
		t.Errorf("footer = %q, want it to name the mode it was switched to", got)
	}
	help := strings.Join(m.helpLines(), "\n")
	if !strings.Contains(help, "stage the hunk the cursor is in") {
		t.Errorf("help screen does not name what s takes in this mode:\n%s", help)
	}
}

// A hunk of what the index already holds is where staging would put it, so there
// is nothing to do and nothing to ask git.
func TestAHunkAlreadyInTheIndexIsLeftAlone(t *testing.T) {
	entries := parseFiles(t, twoHunkFile)
	entries[0].Staged, entries[0].Unstaged = entries[0].Unstaged, nil
	backend := newFakeBackend(sessionOf(entries))
	m := hunkModel(t, backend)

	atHunk(t, m, 0)
	press(t, m, "s")

	if len(backend.stagedHunks) != 0 {
		t.Fatalf("a staged hunk was staged again: %v", backend.stagedHunks)
	}
	if !strings.Contains(m.status, "already") {
		t.Errorf("status = %q, want it to say the hunk is in the index", m.status)
	}
}

// The cursor arrives on a file's header by itself, so the key works from there:
// it takes the change at the top of what the file has left, which is the one
// being read.
func TestTheHunkModeOnAFileHeaderTakesTheFirstHunkLeft(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := hunkModel(t, backend)

	want := m.doc.Hunks[0].ID
	press(t, m, "s")

	if got := backend.stagedHunks; len(got) != 1 || got[0] != want {
		t.Fatalf("StageHunk calls = %v, want the file's first hunk %v", got, want)
	}
	if len(backend.stagedFiles) != 0 {
		t.Errorf("the whole file went in: %v", backend.stagedFiles)
	}
}

// A walkthrough's heading names a group of files rather than any one change, so
// there is nothing under it to stage.
func TestTheHunkModeOnAWalkthroughHeadingStagesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.walkBody = groupedWalkthrough
	m := hunkModel(t, backend)

	press(t, m, "w")
	m.moveTo(m.doc.Steps[0].Row)
	press(t, m, "s")

	if len(backend.stagedHunks) != 0 || len(backend.stagedFiles) != 0 {
		t.Fatalf("a walkthrough heading staged something: %v %v", backend.stagedHunks, backend.stagedFiles)
	}
	if !strings.Contains(m.status, "nothing to stage") {
		t.Errorf("status = %q, want it to say there is nothing there to stage", m.status)
	}
}

// An untracked file has nothing in the index for a patch to apply against, and it
// is all one addition anyway, so the press takes the file — without the reviewer
// having to know which of those two things is the reason.
func TestAnUntrackedFileGoesInWhole(t *testing.T) {
	entries := parseFiles(t, twoHunkFile)
	entries[0].Untracked = true
	backend := newFakeBackend(sessionOf(entries))
	m := hunkModel(t, backend)

	atHunk(t, m, 0)
	press(t, m, "s")

	if len(backend.stagedHunks) != 0 {
		t.Fatalf("a patch was applied to an untracked file: %v", backend.stagedHunks)
	}
	if got := backend.stagedFiles; len(got) != 1 || got[0] != "list.txt" {
		t.Fatalf("StageFile calls = %v, want the whole file", got)
	}
}

// A pull request is not in this working tree, so there is no index to stage a
// hunk of it into — refused on the keypress rather than drawn and taken back.
func TestAHunkOfAReadOnlySessionIsRefused(t *testing.T) {
	session := readOnlySession(t, twoHunkFile)
	backend := newFakeBackend(session)
	m := hunkModel(t, backend)

	atHunk(t, m, 0)
	press(t, m, "s")

	if len(backend.stagedHunks) != 0 {
		t.Fatalf("a read-only session staged a hunk: %v", backend.stagedHunks)
	}
	if m.err == nil {
		t.Error("nothing said why the hunk could not be staged")
	}
}
