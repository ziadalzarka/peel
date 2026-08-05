package store

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestAnchorSurvivesAStoreRewrite(t *testing.T) {
	s := newTestStore(t)

	anchored, err := s.Add(Comment{
		File: "svc.go", Line: 4, Side: SideNew, Origin: OriginWorktree,
		Blob: "6a9da011b757f7800890c7b4afeceb8e79976d6b", Body: "anchored", Author: AuthorUser,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Every mutation rewrites the whole file, so an unrelated write is the
	// moment an anchor would be lost if the field did not round-trip.
	if _, err := s.Add(Comment{File: "other.go", Line: 1, Body: "unrelated", Author: AuthorUser}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s.Update(anchored.ID, func(c *Comment) { c.Resolved = true }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := s.Get(anchored.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Blob != anchored.Blob {
		t.Errorf("blob = %q, want %q kept through the rewrite", got.Blob, anchored.Blob)
	}
}

// TestOutdatedAndMovedAreNeverWrittenDown pins that the two fields worked out on
// read stay out of the file. Storing them would freeze one reading of a working
// tree that goes on changing, and the next process would trust it.
func TestOutdatedAndMovedAreNeverWrittenDown(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Add(Comment{
		File: "svc.go", Line: 4, Body: "note", Author: AuthorUser,
		Blob: "6a9da011b757f7800890c7b4afeceb8e79976d6b", Outdated: true, MovedFrom: 2,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	for _, field := range []string{"outdated", "movedFrom", "Outdated", "MovedFrom"} {
		if strings.Contains(string(raw), field) {
			t.Errorf("the store holds %q; it is worked out on read, not recorded\n%s", field, raw)
		}
	}

	got, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got[0].Outdated || got[0].MovedFrom != 0 {
		t.Errorf("read back outdated %v moved %d, want both clear until worked out",
			got[0].Outdated, got[0].MovedFrom)
	}
}

// TestAStoreFromAnOlderPeelStillReads is the migration question: a comments.json
// written before anchors existed has no blob, and must keep working untouched
// rather than needing a conversion step.
func TestAStoreFromAnOlderPeelStillReads(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(strings.TrimSuffix(s.Path(), "/comments.json"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	old := `{
  "version": 1,
  "comments": [
    {"id":"old1","file":"svc.go","line":4,"side":"new","origin":"worktree",
     "body":"written before anchors","author":"user","resolved":false,
     "createdAt":"2026-07-01T10:00:00Z"}
  ]
}`
	if err := os.WriteFile(s.Path(), []byte(old), 0o644); err != nil {
		t.Fatalf("write old store: %v", err)
	}

	got, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("reading a pre-anchor store: %v", err)
	}
	if len(got) != 1 || got[0].Body != "written before anchors" {
		t.Fatalf("comments = %+v, want the old note read as it was", got)
	}
	if got[0].Blob != "" {
		t.Errorf("blob = %q, want none invented for a note written without one", got[0].Blob)
	}

	// Writing beside it must not disturb the old note or raise the format
	// version — a bumped version is what makes an older peel refuse the file.
	if _, err := s.Add(Comment{File: "svc.go", Line: 9, Body: "new note", Author: AuthorUser,
		Blob: "6a9da011b757f7800890c7b4afeceb8e79976d6b"}); err != nil {
		t.Fatalf("Add beside an old note: %v", err)
	}

	raw, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var f struct {
		Version  int       `json:"version"`
		Comments []Comment `json:"comments"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse rewritten store: %v", err)
	}
	if f.Version != currentVersion {
		t.Errorf("version = %d, want %d — bumping it locks older peels out for an added field",
			f.Version, currentVersion)
	}
	if len(f.Comments) != 2 {
		t.Fatalf("comments = %d, want both", len(f.Comments))
	}
	for _, c := range f.Comments {
		if c.ID == "old1" && c.Blob != "" {
			t.Errorf("the old note gained blob %q; peel cannot know where it was written", c.Blob)
		}
	}
}

// TestAStoreFromANewerPeelIsStillRefused guards the one thing the version is
// for, so adding a field has not quietly disabled it.
func TestAStoreFromANewerPeelIsStillRefused(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(strings.TrimSuffix(s.Path(), "/comments.json"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(s.Path(), []byte(`{"version":99,"comments":[]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.List(Filter{}); err == nil {
		t.Error("a store from a newer peel was read anyway")
	}
}
