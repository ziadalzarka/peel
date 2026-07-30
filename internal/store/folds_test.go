package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFoldsSaveAndLoad(t *testing.T) {
	s := NewJSONFoldStore(filepath.Join(t.TempDir(), "peel", "folds.json"))

	if err := s.Save("", []string{"beta.txt", "alpha.go"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := []string{"alpha.go", "beta.txt"}; !equal(got, want) {
		t.Errorf("Load returned %v, want %v in path order", got, want)
	}
}

// Each review keeps its own folds: a pull request read alongside the working
// tree must not fold the tree's files away.
func TestFoldsAreScopedToTheirTarget(t *testing.T) {
	s := NewJSONFoldStore(filepath.Join(t.TempDir(), "folds.json"))

	if err := s.Save("", []string{"alpha.go"}); err != nil {
		t.Fatalf("Save working tree: %v", err)
	}
	if err := s.Save("github:o/r#412", []string{"pr.go"}); err != nil {
		t.Fatalf("Save pull request: %v", err)
	}

	tree, err := s.Load("")
	if err != nil {
		t.Fatalf("Load working tree: %v", err)
	}
	if !equal(tree, []string{"alpha.go"}) {
		t.Errorf("the working tree folds are %v, want alpha.go alone", tree)
	}
	pr, err := s.Load("github:o/r#412")
	if err != nil {
		t.Fatalf("Load pull request: %v", err)
	}
	if !equal(pr, []string{"pr.go"}) {
		t.Errorf("the pull request folds are %v, want pr.go alone", pr)
	}
}

func TestFoldsLoadMissingFile(t *testing.T) {
	s := NewJSONFoldStore(filepath.Join(t.TempDir(), "folds.json"))

	got, err := s.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load returned %v, want nothing", got)
	}
}

// Folds are a record of what has been read, not data worth failing a review
// over, so a file no build can parse reads back as no folds at all.
func TestFoldsLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "folds.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := NewJSONFoldStore(path)

	got, err := s.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Load returned %v, want nothing", got)
	}
	if err := s.Save("", []string{"alpha.go"}); err != nil {
		t.Fatalf("Save over a corrupt file: %v", err)
	}
}

// Saving nothing forgets the target rather than leaving an empty list behind.
func TestFoldsSaveNothingClearsTheTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "folds.json")
	s := NewJSONFoldStore(path)

	if err := s.Save("", []string{"alpha.go"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save("", nil); err != nil {
		t.Fatalf("Save nothing: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(b), "alpha.go") {
		t.Errorf("the file still holds the cleared fold: %s", b)
	}
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
