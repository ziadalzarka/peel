package store_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

func TestReviewStoreKeepsRunsBesideItsNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "412.json")
	review := store.NewReviewStore(path, "github:o/r#412")
	if _, err := review.Comments().Add(store.Comment{File: "a.go", Line: 1, Body: "note", Author: "claude"}); err != nil {
		t.Fatalf("Add comment: %v", err)
	}
	kept, err := review.Hidden().Add(store.HiddenRun{File: "a.go", Line: 2, EndLine: 3, Author: "claude"})
	if err != nil {
		t.Fatalf("Add run: %v", err)
	}
	other, err := review.Hidden().Add(store.HiddenRun{File: "b.go", Author: "someone"})
	if err != nil {
		t.Fatalf("Add run: %v", err)
	}

	again := store.NewReviewStore(path, "github:o/r#412")
	runs, err := again.Hidden().List(store.HiddenFilter{Author: "claude"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != kept.ID {
		t.Errorf("runs = %+v, want the one signed claude", runs)
	}

	n, err := again.Hidden().Clear(store.HiddenFilter{Author: "claude"})
	if err != nil || n != 1 {
		t.Errorf("Clear = %d, %v, want 1", n, err)
	}
	if err := again.Hidden().Remove(other.ID); err != nil {
		t.Errorf("Remove: %v", err)
	}
	if err := again.Hidden().Remove(other.ID); !errors.Is(err, store.ErrRunNotFound) {
		t.Errorf("Remove twice = %v, want not found", err)
	}
	notes, err := again.Comments().List(store.Filter{})
	if err != nil || len(notes) != 1 {
		t.Errorf("notes = %+v, %v, want the note left alone", notes, err)
	}
}

func TestAJumpSurvivesBeingReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "412.jump")
	empty, err := store.ReadJump(path)
	if err != nil || !empty.At.IsZero() {
		t.Fatalf("ReadJump before any = %+v, %v", empty, err)
	}
	want := store.Jump{File: "a.go", Line: 9}
	if err := store.WriteJump(path, want); err != nil {
		t.Fatalf("WriteJump: %v", err)
	}
	got, err := store.ReadJump(path)
	if err != nil || got.File != "a.go" || got.Line != 9 {
		t.Errorf("ReadJump = %+v, %v", got, err)
	}
}
