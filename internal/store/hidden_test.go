package store_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

func hiddenStore(t *testing.T) *store.JSONHiddenRunStore {
	t.Helper()
	return store.NewJSONHiddenRunStore(filepath.Join(t.TempDir(), "hidden.json"))
}

func TestHiddenRunsAreKeptInFileAndLineOrder(t *testing.T) {
	s := hiddenStore(t)
	for _, r := range []store.HiddenRun{
		{File: "b.go", Line: 1, Author: "claude"},
		{File: "a.go", Line: 9, Author: "claude"},
		{File: "a.go", Line: 2, EndLine: 4, Label: "body", Author: "codex"},
	} {
		if _, err := s.Add(r); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	all, err := s.List(store.HiddenFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"a.go:2-4", "a.go:9", "b.go:1"}
	for i, r := range all {
		if r.Location() != want[i] {
			t.Errorf("run %d is %s, want %s", i, r.Location(), want[i])
		}
		if r.ID == "" || r.CreatedAt.IsZero() {
			t.Errorf("run %d was not given an id and a time: %+v", i, r)
		}
	}

	byAuthor, _ := s.List(store.HiddenFilter{Author: "codex"})
	if len(byAuthor) != 1 || byAuthor[0].Label != "body" {
		t.Errorf("author filter returned %+v", byAuthor)
	}
	byFile, _ := s.List(store.HiddenFilter{File: "b.go"})
	if len(byFile) != 1 {
		t.Errorf("file filter returned %+v", byFile)
	}
}

func TestAHiddenRunWithNoAuthorIsSignedUnknown(t *testing.T) {
	got, err := hiddenStore(t).Add(store.HiddenRun{File: "a.go"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.Author != store.AuthorUnknown || !got.WholeFile() {
		t.Errorf("got %+v, want a whole-file run signed unknown", got)
	}
}

func TestHiddenRunsRejectLinesThatAreNotARange(t *testing.T) {
	s := hiddenStore(t)
	for name, r := range map[string]store.HiddenRun{
		"no file":    {Line: 1},
		"negative":   {File: "a.go", Line: -1},
		"end only":   {File: "a.go", EndLine: 4},
		"end before": {File: "a.go", Line: 5, EndLine: 4},
		"not a name": {File: "a.go", Author: " claude"},
	} {
		if _, err := s.Add(r); err == nil {
			t.Errorf("%s: Add accepted %+v", name, r)
		}
	}
}

func TestRemovingAndClearingHiddenRuns(t *testing.T) {
	s := hiddenStore(t)
	keep, _ := s.Add(store.HiddenRun{File: "a.go", Line: 1, Author: "ziad"})
	gone, _ := s.Add(store.HiddenRun{File: "a.go", Line: 2, Author: "claude"})
	if _, err := s.Add(store.HiddenRun{File: "b.go", Line: 3, Author: "claude"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := s.Remove(gone.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := s.Remove(gone.ID); !errors.Is(err, store.ErrRunNotFound) {
		t.Errorf("removing it twice = %v, want not found", err)
	}

	n, err := s.Clear(store.HiddenFilter{Author: "claude"})
	if err != nil || n != 1 {
		t.Fatalf("Clear = %d, %v, want 1", n, err)
	}
	left, _ := s.List(store.HiddenFilter{})
	if len(left) != 1 || left[0].ID != keep.ID {
		t.Errorf("left %+v, want only %s", left, keep.ID)
	}
}
