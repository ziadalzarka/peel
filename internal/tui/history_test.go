package tui

import (
	"errors"
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/gittest"
	"github.com/ziadalzarka/peel/internal/store"
)

func TestUndoOpensAFileFoldedByHand(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))
	path := m.doc.Files[0].Entry.Path

	press(t, m, "space")
	if !m.collapsed[path] {
		t.Fatalf("space left %s open, so there is no fold to take back", path)
	}

	press(t, m, "z")
	if m.collapsed[path] {
		t.Errorf("%s is still folded after cmd+z", path)
	}
	if m.status != "undone: folding "+path {
		t.Errorf("status = %q, want it to name what was taken back", m.status)
	}

	press(t, m, "Z")
	if !m.collapsed[path] {
		t.Errorf("%s is open again after cmd+shift+z, want it folded", path)
	}
	if m.status != "redone: folding "+path {
		t.Errorf("status = %q, want it to name what was put back", m.status)
	}
}

func TestUndoPutsTheCursorBackWhereTheFoldWasPressed(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, threeFileDiff)))
	press(t, m, "down", "down")
	at, file := m.cursor, m.doc.FileAt(m.cursor)

	press(t, m, "space")
	if m.doc.FileAt(m.cursor) == file {
		t.Fatalf("folding left the cursor in file %d — it is meant to move on", file)
	}

	press(t, m, "z")
	if m.cursor != at || m.doc.FileAt(m.cursor) != file {
		t.Errorf("cursor = %d in file %d after undo, want row %d in file %d where space was pressed",
			m.cursor, m.doc.FileAt(m.cursor), at, file)
	}
}

func TestUndoTakesBackOneFoldAtATime(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, threeFileDiff)))
	first := m.doc.Files[0].Entry.Path

	press(t, m, "space")
	second := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path
	press(t, m, "space")
	if !m.collapsed[first] || !m.collapsed[second] {
		t.Fatalf("want both %s and %s folded, got %v", first, second, m.collapsed)
	}

	press(t, m, "z")
	if m.collapsed[second] {
		t.Errorf("%s is still folded, want the newest fold taken back first", second)
	}
	if !m.collapsed[first] {
		t.Errorf("%s came open too — undo takes back one press", first)
	}

	press(t, m, "z")
	if m.collapsed[first] {
		t.Errorf("%s is still folded after the second undo", first)
	}
}

func TestUndoSaysWhenThereIsNothingToTakeBack(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "z")
	if m.status != "nothing to undo" {
		t.Errorf("status = %q, want it to say there is nothing to undo", m.status)
	}
	press(t, m, "Z")
	if m.status != "nothing to redo" {
		t.Errorf("status = %q, want it to say there is nothing to redo", m.status)
	}
}

func TestAPressAfterUndoLeavesNothingToRedo(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, threeFileDiff)))

	press(t, m, "space", "z")
	press(t, m, "down", "space")
	press(t, m, "Z")
	if m.status != "nothing to redo" {
		t.Errorf("status = %q, want the redo dropped by the fold pressed after the undo", m.status)
	}
}

func TestUndoTakesBackFoldingACommentAndTheHalfAlreadyStaged(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "look here", Author: store.AuthorUser},
	}
	m := newModel(t, backend)

	row := m.doc.RowOfComment("c1")
	if row < 0 {
		t.Fatal("the note is not in the diff")
	}
	m.moveTo(row)
	press(t, m, "space")
	if !m.commentFolds["c1"] {
		t.Fatalf("space left the note open, so there is no fold to take back")
	}

	press(t, m, "z")
	if m.commentFolds["c1"] {
		t.Errorf("the note is still folded after undo")
	}
}

func TestUndoOfAStagedFilePutsItBackInTheWorkingTree(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)
	path := m.doc.Files[0].Entry.Path

	press(t, m, "s")
	if got := backend.stagedFiles; len(got) != 1 || got[0] != path {
		t.Fatalf("staged %v, want just %s", got, path)
	}
	after := backend.indexTree

	press(t, m, "z")
	if len(backend.restored) != 1 {
		t.Fatalf("the index was put back %d times, want once", len(backend.restored))
	}
	if got := backend.restored[0]; got[0] != after || got[1] != "tree0" {
		t.Errorf("put the index back from %q to %q, want from %q to the tree before the press", got[0], got[1], after)
	}
	if m.status != "undone: staged "+path {
		t.Errorf("status = %q, want it to name the staging taken back", m.status)
	}
	if m.collapsed[path] {
		t.Errorf("%s is still folded away, want it open again with its change", path)
	}
}

func TestRedoStagesTheFileAgain(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)
	path := m.doc.Files[0].Entry.Path

	press(t, m, "s")
	staged := backend.indexTree
	press(t, m, "z")
	press(t, m, "Z")

	if len(backend.restored) != 2 {
		t.Fatalf("the index was put back %d times, want once for the undo and once for the redo", len(backend.restored))
	}
	if got := backend.restored[1]; got[1] != staged {
		t.Errorf("redo left the index at %q, want the tree staging made at %q", got[1], staged)
	}
	if !m.collapsed[path] {
		t.Errorf("%s is open after redo, want it folded away again", path)
	}
}

func TestUndoRefusesWhenTheIndexHasMovedSince(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "s")
	// Somebody else stages something while the review is open.
	backend.moveIndex()

	press(t, m, "z")
	if m.err == nil {
		t.Fatal("undo said nothing about an index that has moved under it")
	}
	if len(backend.restored) != 0 {
		t.Errorf("the index was put back anyway: %v", backend.restored)
	}

	// A refusal is not a press taken back, so the staging is still there to take
	// back once the index is somewhere it can be taken back from.
	press(t, m, "z")
	if m.status == "nothing to undo" {
		t.Error("the refused press was dropped from the history")
	}
}

func TestUndoOfACommentRemovesIt(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	press(t, m, "down", "down", "c")
	typeText(t, m, "needs a test")
	press(t, m, "enter")
	if len(m.comments) != 1 {
		t.Fatalf("the note is not in the diff: %v", m.comments)
	}
	id := m.comments[0].ID

	press(t, m, "z")
	if len(m.comments) != 0 {
		t.Errorf("the note is still there after undo: %v", m.comments)
	}
	press(t, m, "Z")
	if len(m.comments) != 1 {
		t.Fatalf("redo did not write the note again: %v", m.comments)
	}
	if got := m.comments[0].ID; got != id {
		t.Errorf("the note came back as %q, want the %q it was written as", got, id)
	}
}

func TestUndoOfADeletedCommentWritesItBackWhole(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "look here",
			Author: store.AuthorUser, Blob: "abc123"},
	}
	m := newModel(t, backend)
	m.moveTo(m.doc.RowOfComment("c1"))

	press(t, m, "D")
	if len(m.comments) != 0 {
		t.Fatalf("D left the note in the diff: %v", m.comments)
	}

	press(t, m, "z")
	if len(m.comments) != 1 {
		t.Fatalf("undo did not put the note back: %v", m.comments)
	}
	got := m.comments[0]
	if got.ID != "c1" || got.Body != "look here" || got.Blob != "abc123" {
		t.Errorf("the note came back as %+v, want the one that was deleted", got)
	}
}

func TestUndoOfAnEditPutsTheOldWordsBack(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "first words", Author: store.AuthorUser},
	}
	m := newModel(t, backend)
	m.moveTo(m.doc.RowOfComment("c1"))

	press(t, m, "e")
	typeText(t, m, " and more")
	press(t, m, "enter")
	if got := m.comments[0].Body; got != "first words and more" {
		t.Fatalf("the note says %q, so there is no edit to take back", got)
	}

	press(t, m, "z")
	if got := m.comments[0].Body; got != "first words" {
		t.Errorf("the note says %q after undo, want the words it had before the edit", got)
	}
}

func TestUndoOfAResolveReopensTheComment(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "look here", Author: store.AuthorUser},
	}
	m := newModel(t, backend)
	m.moveTo(m.doc.RowOfComment("c1"))

	press(t, m, "x")
	if !m.comments[0].Resolved {
		t.Fatal("x left the note open, so there is nothing to take back")
	}

	press(t, m, "z")
	if m.comments[0].Resolved {
		t.Errorf("the note is still resolved after undo")
	}
}

func TestAChangeThatFailedToWriteIsNotOnTheStack(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.opErr = errors.New("git said no")
	m := newModel(t, backend)

	press(t, m, "s")
	if m.err == nil {
		t.Fatal("staging reported no error, so this test is not exercising a failed write")
	}

	press(t, m, "z")
	if m.status != "nothing to undo" {
		t.Errorf("status = %q, want the failed press left off the stack", m.status)
	}
	if len(backend.restored) != 0 {
		t.Errorf("the index was put back for a change that never landed: %v", backend.restored)
	}
}

func TestUndoOfAStagedHunkPutsTheIndexBack(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoHunkFile))
	m := hunkModel(t, backend)
	atHunk(t, m, 0)

	press(t, m, "s")
	if len(backend.stagedHunks) != 1 {
		t.Fatalf("staged %d hunks, want the one the cursor was in", len(backend.stagedHunks))
	}
	staged := backend.indexTree

	press(t, m, "z")
	if len(backend.restored) != 1 {
		t.Fatalf("the index was put back %d times, want once", len(backend.restored))
	}
	if got := backend.restored[0]; got[0] != staged || got[1] != "tree0" {
		t.Errorf("put the index back from %q to %q, want from %q to tree0", got[0], got[1], staged)
	}
}

func TestUndoOfStagingEverythingOpensItAllAgain(t *testing.T) {
	backend := newFakeBackend(newSession(t, threeFileDiff))
	m := newModel(t, backend)

	press(t, m, "a")
	if backend.stageAll != 1 {
		t.Fatalf("stage all ran %d times, want once", backend.stageAll)
	}
	if len(m.collapsed) == 0 {
		t.Fatal("a left every file open, so there is no folding to take back")
	}

	press(t, m, "z")
	if len(m.collapsed) != 0 {
		t.Errorf("%d files are still folded after undo, want them all open", len(m.collapsed))
	}
	if len(backend.restored) != 1 {
		t.Errorf("the index was put back %d times, want once", len(backend.restored))
	}
}

func TestUndoOfClearingOthersWritesThemBackAndShowsThem(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "theirs", Author: "claude"},
		{ID: "c2", File: "beta.txt", Line: 2, Side: store.SideNew, Body: "also theirs", Author: "claude"},
	}
	m := newModel(t, backend)

	press(t, m, "X")
	press(t, m, "y")
	if len(m.comments) != 0 {
		t.Fatalf("X left %d notes in the diff", len(m.comments))
	}

	press(t, m, "z")
	if len(m.comments) != 2 {
		t.Fatalf("undo put back %d notes, want both", len(m.comments))
	}
	if len(backend.added) != 2 {
		t.Errorf("the store was written %d notes, want both back", len(backend.added))
	}
}

func TestUndoRefusesAnEditSomebodyElseHasRewritten(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Side: store.SideNew, Body: "first words", Author: store.AuthorUser},
	}
	m := newModel(t, backend)
	m.moveTo(m.doc.RowOfComment("c1"))

	press(t, m, "e")
	typeText(t, m, " and more")
	press(t, m, "enter")

	// An agent answering the review rewrites the same note while it is open.
	backend.comments[0].Body = "what the agent wrote"

	press(t, m, "z")
	if m.err == nil {
		t.Fatal("undo rewrote a note that had moved on under it")
	}
	if got := backend.comments[0].Body; got != "what the agent wrote" {
		t.Errorf("the note says %q, want the agent's words left alone", got)
	}
	press(t, m, "z")
	if m.status == "nothing to undo" {
		t.Error("the refused press was dropped from the history")
	}
}

func TestCmdZUndoesAndCmdShiftZRedoes(t *testing.T) {
	for _, tc := range []struct {
		name string
		undo string
		redo string
	}{
		{"kitty", "\x1b[122;9u", "\x1b[122;10u"},
		{"with an event type", "\x1b[122;9:1u", "\x1b[122;10:1u"},
		{"reporting the shifted key too", "\x1b[122:90;9u", "\x1b[122:90;10u"},
		{"modifyOtherKeys", "\x1b[27;9;122~", "\x1b[27;10;122~"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))
			path := m.doc.Files[0].Entry.Path
			press(t, m, "space")

			send(t, m, csiSequenceMsg(tc.undo))
			if m.collapsed[path] {
				t.Errorf("%q left %s folded, want the fold taken back", tc.undo, path)
			}
			send(t, m, csiSequenceMsg(tc.redo))
			if !m.collapsed[path] {
				t.Errorf("%q left %s open, want the fold put again", tc.redo, path)
			}
		})
	}
}

// Against real git: taking back a staging puts the index back where it was and
// leaves the file on disk exactly as it is.
func TestUndoTakesTheFileBackOutOfTheIndex(t *testing.T) {
	repo := gittest.New(t)
	repo.Write("list.txt", "one\ntwo\nthree\n")
	repo.Commit("base")
	repo.Write("list.txt", "ONE\ntwo\nTHREE\n")

	m := realModel(t, repo)
	press(t, m, "s")
	if got := repo.StagedRaw("list.txt"); got != "ONE\ntwo\nTHREE\n" {
		t.Fatalf("index contents = %q, so there is no staging to take back", got)
	}

	press(t, m, "z")
	if got := repo.StagedRaw("list.txt"); got != "one\ntwo\nthree\n" {
		t.Errorf("index contents = %q, want the committed file back", got)
	}
	if got := repo.Read("list.txt"); got != "ONE\ntwo\nTHREE\n" {
		t.Errorf("the working tree says %q — undo is not allowed to touch it", got)
	}
	if got := repo.StatusLines(); len(got) != 1 || got[0] != " M list.txt" {
		t.Errorf("status = %v, want the change back out of the index alone", got)
	}

	press(t, m, "Z")
	if got := repo.StagedRaw("list.txt"); got != "ONE\ntwo\nTHREE\n" {
		t.Errorf("index contents = %q after redo, want the change staged again", got)
	}
}

// An untracked file goes into the index as a whole file, and comes back out of
// it as one: the path git had never heard of is unknown to it again, and the
// file is still on disk.
func TestUndoTakesAnUntrackedFileBackOutOfTheIndex(t *testing.T) {
	repo := gittest.New(t)
	repo.Write("kept.txt", "one\n")
	repo.Commit("base")
	repo.Write("new.txt", "fresh\n")

	m := realModel(t, repo)
	press(t, m, "s")
	if got := repo.StatusLines(); len(got) != 1 || got[0] != "A  new.txt" {
		t.Fatalf("status = %v, want the new file staged", got)
	}

	press(t, m, "z")
	if got := repo.StatusLines(); len(got) != 1 || got[0] != "?? new.txt" {
		t.Errorf("status = %v, want the file untracked again", got)
	}
	if got := repo.Read("new.txt"); got != "fresh\n" {
		t.Errorf("the file on disk says %q, want it untouched", got)
	}
}

// Against real git: one hunk staged and taken back leaves the index exactly as
// it was, with the rest of the file still out of it.
func TestUndoTakesAStagedHunkBackOutOfTheIndex(t *testing.T) {
	const before = "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\ntwelve\n"
	const after = "ONE\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\neleven\nTWELVE\n"

	repo := gittest.New(t)
	repo.Write("list.txt", before)
	repo.Commit("base")
	repo.Write("list.txt", after)

	m := realModel(t, repo, WithStageMode(app.StageModeHunk))
	atHunk(t, m, 0)
	press(t, m, "s")
	staged := repo.StagedRaw("list.txt")
	if staged == before || staged == after {
		t.Fatalf("index contents = %q, want one hunk of the change", staged)
	}

	press(t, m, "z")
	if got := repo.StagedRaw("list.txt"); got != before {
		t.Errorf("index contents = %q, want the committed file back", got)
	}
	if got := repo.Read("list.txt"); got != after {
		t.Errorf("the working tree says %q — undo is not allowed to touch it", got)
	}
}

// Against real git and the real store: a note deleted and taken back is the
// note that was written, down to the name the store gave it.
func TestUndoWritesADeletedNoteBackToTheStore(t *testing.T) {
	repo := gittest.New(t)
	repo.Write("list.txt", "one\ntwo\nthree\n")
	repo.Commit("base")
	repo.Write("list.txt", "ONE\ntwo\nthree\n")

	m := realModel(t, repo)
	m.moveTo(lineRowOf(t, m, 0, 1))
	press(t, m, "c")
	typeText(t, m, "why upper case")
	press(t, m, "enter")
	if len(m.comments) != 1 {
		t.Fatalf("the note was not written: %v", m.comments)
	}
	was := m.comments[0]

	m.moveTo(m.doc.RowOfComment(was.ID))
	press(t, m, "D")
	if len(m.comments) != 0 {
		t.Fatalf("D left the note in the diff: %v", m.comments)
	}

	press(t, m, "z")
	if len(m.comments) != 1 {
		t.Fatalf("undo did not put the note back: %v", m.comments)
	}
	got := m.comments[0]
	if got.ID != was.ID || got.Body != was.Body || got.Line != was.Line || got.Blob != was.Blob {
		t.Errorf("the note came back as %+v, want the %+v that was deleted", got, was)
	}
}

// A fold taken back puts back the one fold it was, and leaves alone a file a
// reload has opened because new work landed in it while the review was up.
func TestUndoOfAFoldLeavesAFileReopenedByAReloadOpen(t *testing.T) {
	backend := newFakeBackend(newSession(t, threeFileDiff))
	m := newModel(t, backend)

	first := m.doc.Files[0].Entry.Path
	press(t, m, "s")
	if !m.collapsed[first] {
		t.Fatalf("%s did not fold away when it was staged", first)
	}

	second := m.doc.Files[m.doc.FileAt(m.cursor)].Entry.Path
	press(t, m, "space")
	if !m.collapsed[second] {
		t.Fatalf("space left %s open", second)
	}

	// The reload a follow check runs finds new work in the staged file, so it
	// opens it again: what arrived has not been read.
	m.setCollapsed(unfold(first))
	delete(m.stagedFolds, first)

	press(t, m, "z")
	if m.collapsed[first] {
		t.Errorf("%s was folded away again by taking back a fold of %s — its new work is hidden", first, second)
	}
	if m.collapsed[second] {
		t.Errorf("%s is still folded, so the fold was not taken back at all", second)
	}
}

// A press whose write is still out has no inverse yet, since the inverse is
// what the write hands back. It stays on the stack and says so.
func TestUndoWaitsForAWriteStillOut(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	_, cmd := m.Update(keyMsg("s"))
	press(t, m, "z")
	if m.status != "still writing staged alpha.go — try that again in a moment" {
		t.Errorf("status = %q, want it to say the write is still out", m.status)
	}

	settle(t, m, cmd, 0)
	press(t, m, "z")
	if len(backend.restored) != 1 {
		t.Errorf("the index was put back %d times once the write had landed, want once", len(backend.restored))
	}
}
