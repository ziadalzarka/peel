package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
)

// conflictDiff is what peel reads for an unmerged path: the merge git left on
// disk, measured against the last commit, markers and all.
const conflictDiff = `diff --git a/f.txt b/f.txt
--- a/f.txt
+++ b/f.txt
@@ -1,4 +1,8 @@
 a
 b
+<<<<<<< HEAD
 OURS
+=======
+THEIRS
+>>>>>>> side
 d
`

func conflictSession(t *testing.T) *app.Session {
	t.Helper()
	entries := parseFiles(t, conflictDiff)
	entries[0].Conflicted = true
	return sessionOf(entries)
}

func TestConflictedFileSaysWhatItIsAboveTheDiff(t *testing.T) {
	doc := Build(conflictSession(t), nil, nil, LayoutUnified)

	var notes []string
	for _, row := range doc.Rows {
		if row.Kind == RowNote {
			notes = append(notes, row.Text)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "merge conflict") {
		t.Fatalf("notes = %v, want one naming the merge conflict", notes)
	}

	// It has to come before the code it explains, or it explains nothing.
	noteAt, lineAt := -1, -1
	for i, row := range doc.Rows {
		if row.Kind == RowNote && noteAt < 0 {
			noteAt = i
		}
		if row.Kind == RowLine && lineAt < 0 {
			lineAt = i
		}
	}
	if noteAt < 0 || lineAt < 0 || noteAt > lineAt {
		t.Errorf("note at row %d, first diff line at row %d — want the note first", noteAt, lineAt)
	}
}

func TestConflictedFileHeaderSaysConflicted(t *testing.T) {
	session := conflictSession(t)
	doc := Build(session, nil, nil, LayoutUnified)
	r := plainRenderer(80)

	var header string
	for i, row := range doc.Rows {
		if row.Kind == RowFile {
			header = r.Row(doc, i, RowState{})
			break
		}
	}
	if !strings.Contains(header, "conflicted") {
		t.Errorf("file header = %q, want it to say conflicted", header)
	}
}

func TestConflictedFileWithNothingToDiffStillSaysWhy(t *testing.T) {
	// Ours kept, theirs deleted: no diff at all, and the note is the only thing
	// telling the reviewer the file is waiting on a decision.
	session := sessionOf([]git.FileEntry{{Path: "f.txt", Conflicted: true}})
	doc := Build(session, nil, nil, LayoutUnified)

	var notes []string
	for _, row := range doc.Rows {
		if row.Kind == RowNote {
			notes = append(notes, row.Text)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "merge conflict") {
		t.Fatalf("notes = %v, want one naming the merge conflict", notes)
	}
}

func TestStagingAConflictClearsIt(t *testing.T) {
	// The screen is drawn ahead of the write, and a staged conflict is a
	// resolved one — so the guess must not go on calling it conflicted.
	session := conflictSession(t)
	out := restaged(session, true, func(path string) bool { return path == "f.txt" })

	if out.Files[0].Conflicted {
		t.Error("file still reads as conflicted after being staged")
	}
	if out.Files[0].State() != git.StateStaged {
		t.Errorf("State() = %v, want staged", out.Files[0].State())
	}
}

func TestStagingWorksWhileAnotherPathIsConflicted(t *testing.T) {
	// `git write-tree` refuses an index with any unmerged path in it, and that
	// is the call staging makes to record what a press can be taken back to.
	// Mid-merge it fails for every file, conflicted or not, so the press has to
	// go through without an undo rather than not go through at all.
	backend := newFakeBackend(newSession(t, twoFileDiff))
	backend.indexTreeErr = errors.New("not a fully merged index")
	m := newModel(t, backend)
	path := m.doc.Files[0].Entry.Path

	press(t, m, "s")
	if got := backend.stagedFiles; len(got) != 1 || got[0] != path {
		t.Fatalf("staged %v, want %s staged even with no tree to record", got, path)
	}
	if m.err != nil {
		t.Errorf("staging reported %v, want it to go through quietly", m.err)
	}

	press(t, m, "z")
	if m.status != "nothing to undo" {
		t.Errorf("status = %q, want the press to be one there is no undo for", m.status)
	}
}
