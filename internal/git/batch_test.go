package git_test

import (
	"testing"
)

// Working out where a review's notes sit asks what every version of every
// commented file holds right now. Asked one at a time that is a process per
// note, on every re-read, in a mode that re-reads continuously — so both of
// these answer for a whole review at once, and the pairing of question to
// answer is the thing that has to hold.

func TestBlobsAnswersEveryNameAskedInOrder(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", "first\n")
	h.fixture.Write("b.txt", "second\n")
	h.fixture.Commit("base")
	h.fixture.Write("b.txt", "second, staged\n")
	h.fixture.Git("add", "b.txt")

	got, err := h.repo.Blobs(h.ctx, []string{":a.txt", "HEAD:b.txt", ":b.txt"})
	if err != nil {
		t.Fatalf("Blobs: %v", err)
	}

	want := map[string]string{
		":a.txt":     h.fixture.Git("rev-parse", ":a.txt"),
		"HEAD:b.txt": h.fixture.Git("rev-parse", "HEAD:b.txt"),
		":b.txt":     h.fixture.Git("rev-parse", ":b.txt"),
	}
	for spec, blob := range want {
		if got[spec] != blob {
			t.Errorf("%s = %s, want %s", spec, got[spec], blob)
		}
	}
	// The staged copy and the committed one are different objects, so a batch
	// that answered the same thing twice would look right without being right.
	if want["HEAD:b.txt"] == want[":b.txt"] {
		t.Fatal("the fixture staged nothing; this test is not exercising anything")
	}
}

// A name git does not hold answers "<name> missing" on its own line, so it must
// not slide the answers along and hand the next name's blob to this one.
func TestBlobsSkipsANameGitDoesNotHold(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", "first\n")
	h.fixture.Commit("base")

	got, err := h.repo.Blobs(h.ctx, []string{":gone.txt", ":a.txt", ":also-gone.txt"})
	if err != nil {
		t.Fatalf("Blobs: %v", err)
	}
	if _, ok := got[":gone.txt"]; ok {
		t.Error("a name the repository does not hold came back with a blob")
	}
	if want := h.fixture.Git("rev-parse", ":a.txt"); got[":a.txt"] != want {
		t.Errorf(":a.txt = %s, want %s — the misses shifted the answers", got[":a.txt"], want)
	}
}

func TestBlobsAsksNothingForNothing(t *testing.T) {
	h := newHarness(t)
	got, err := h.repo.Blobs(h.ctx, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("Blobs(nil) = %v, %v; want no answers and no error", got, err)
	}
}

// HashFiles is how a snapshot is told apart from the file on disk without
// diffing them, so the hash it returns has to be the one the snapshot was
// written under.
func TestHashFilesMatchesTheSnapshotOfTheSameFile(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", "first\n")
	h.fixture.Write("b.txt", "second\n")
	h.fixture.Commit("base")

	snapshot, err := h.repo.SnapshotFile(h.ctx, "a.txt")
	if err != nil {
		t.Fatalf("SnapshotFile: %v", err)
	}

	got, err := h.repo.HashFiles(h.ctx, []string{"a.txt", "b.txt"})
	if err != nil {
		t.Fatalf("HashFiles: %v", err)
	}
	if got["a.txt"] != snapshot {
		t.Errorf("a.txt hashes to %s, want the %s it was snapshotted as", got["a.txt"], snapshot)
	}
	if got["b.txt"] == got["a.txt"] {
		t.Error("two different files hashed the same")
	}
}

// A file that has gone since the note was written cannot be hashed, and git
// stops at the first path it cannot read — so one missing file must not cost
// the answers for the ones that are there.
func TestHashFilesSkipsAFileThatIsNotThere(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", "first\n")
	h.fixture.Commit("base")

	got, err := h.repo.HashFiles(h.ctx, []string{"gone.txt", "a.txt"})
	if err != nil {
		t.Fatalf("HashFiles: %v", err)
	}
	if _, ok := got["gone.txt"]; ok {
		t.Error("a file that is not there came back with a hash")
	}
	if got["a.txt"] == "" {
		t.Error("the missing file took the real one's answer with it")
	}
}

// The hash follows the file on disk rather than what the index holds, which is
// the whole reason it is asked.
func TestHashFilesReadsTheWorkingTreeNotTheIndex(t *testing.T) {
	h := newHarness(t)
	h.fixture.Write("a.txt", "first\n")
	h.fixture.Commit("base")
	h.fixture.Write("a.txt", "edited on disk\n")

	got, err := h.repo.HashFiles(h.ctx, []string{"a.txt"})
	if err != nil {
		t.Fatalf("HashFiles: %v", err)
	}
	if staged := h.fixture.Git("rev-parse", ":a.txt"); got["a.txt"] == staged {
		t.Error("the hash came from the index; it must come from the file on disk")
	}
}
