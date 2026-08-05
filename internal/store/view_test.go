package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestViewSaveAndLoad(t *testing.T) {
	s := NewJSONViewStore(filepath.Join(t.TempDir(), "peel", "view.json"))

	if err := s.Save("", View{AgentCommentsHidden: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !got.AgentCommentsHidden {
		t.Error("the agent's notes read back as shown, and they were hidden")
	}
}

// Each review is looked at on its own terms: hiding an agent's notes on a pull
// request says nothing about the ones on the working tree.
func TestViewsAreScopedToTheirTarget(t *testing.T) {
	s := NewJSONViewStore(filepath.Join(t.TempDir(), "view.json"))

	if err := s.Save("github:o/r#412", View{AgentCommentsHidden: true}); err != nil {
		t.Fatalf("Save pull request: %v", err)
	}

	tree, err := s.Load("")
	if err != nil {
		t.Fatalf("Load working tree: %v", err)
	}
	if tree.AgentCommentsHidden {
		t.Error("the working tree hid the agent's notes, and only the pull request did")
	}
	pr, err := s.Load("github:o/r#412")
	if err != nil {
		t.Fatalf("Load pull request: %v", err)
	}
	if !pr.AgentCommentsHidden {
		t.Error("the pull request lost the filter it was left with")
	}
}

func TestViewLoadMissingFile(t *testing.T) {
	s := NewJSONViewStore(filepath.Join(t.TempDir(), "view.json"))

	got, err := s.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != (View{}) {
		t.Errorf("Load returned %+v, want the default view", got)
	}
}

// How a diff was being looked at is not worth failing a review over, so a file
// no build can parse reads back as the default and is written over.
func TestViewLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "view.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	s := NewJSONViewStore(path)

	got, err := s.Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got != (View{}) {
		t.Errorf("Load returned %+v, want the default view", got)
	}
	if err := s.Save("", View{AgentCommentsHidden: true}); err != nil {
		t.Fatalf("Save over a corrupt file: %v", err)
	}
}

// A review back at the default is forgotten rather than written out.
func TestViewSaveTheDefaultClearsTheTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "view.json")
	s := NewJSONViewStore(path)

	if err := s.Save("github:o/r#412", View{AgentCommentsHidden: true}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Save("github:o/r#412", View{}); err != nil {
		t.Fatalf("Save the default: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(b), "github:o/r#412") {
		t.Errorf("the file still holds the cleared view: %s", b)
	}
}
