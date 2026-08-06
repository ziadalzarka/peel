package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testTarget = "github:cli/cli#412"

// newTestReview returns a review store with a deterministic clock and IDs, at a
// path named the way a pull request's review is named.
func newTestReview(t *testing.T) *ReviewStore {
	t.Helper()
	var n int
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	path := filepath.Join(t.TempDir(), "reviews", "github", "cli", "cli", "412.json")
	return NewReviewStore(path, testTarget,
		WithIDGenerator(func() string { n++; return "id" + string(rune('0'+n)) }),
		WithClock(func() time.Time { return base.Add(time.Duration(n) * time.Minute) }),
	)
}

// One file holds the whole review, so everything written to it is still there
// when the next thing is written beside it.
func TestReviewStoreHoldsEveryPartOfOneReview(t *testing.T) {
	s := newTestReview(t)

	if _, err := s.Comments().Add(Comment{File: "pr.go", Line: 3, Body: "this leaks"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Folds().Save(testTarget, []string{"z.go", "a.go"}); err != nil {
		t.Fatalf("Save folds: %v", err)
	}
	if err := s.Views().Save(testTarget, View{AgentCommentsHidden: true}); err != nil {
		t.Fatalf("Save view: %v", err)
	}
	if err := s.Walkthroughs().Save(Walkthrough{Target: testTarget, Fingerprint: "abc", Body: "## 1. It"}); err != nil {
		t.Fatalf("Save walkthrough: %v", err)
	}

	comments, err := s.Comments().List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "this leaks" {
		t.Fatalf("comments = %v", comments)
	}
	// A note in this file is a note on this review, whatever the caller said.
	if comments[0].Target != testTarget {
		t.Errorf("Target = %q, want the review the file holds", comments[0].Target)
	}

	folded, err := s.Folds().Load(testTarget)
	if err != nil {
		t.Fatalf("Load folds: %v", err)
	}
	if len(folded) != 2 || folded[0] != "a.go" {
		t.Errorf("folded = %v, want them in path order", folded)
	}

	view, err := s.Views().Load(testTarget)
	if err != nil {
		t.Fatalf("Load view: %v", err)
	}
	if !view.AgentCommentsHidden {
		t.Error("the view did not survive the writes beside it")
	}

	walk, ok, err := s.Walkthroughs().Load()
	if err != nil || !ok {
		t.Fatalf("Load walkthrough: ok=%v err=%v", ok, err)
	}
	if !walk.Fresh(testTarget, "abc") {
		t.Errorf("walkthrough = %+v, want the one saved", walk)
	}
	if walk.CreatedAt.IsZero() {
		t.Error("the walkthrough was cached without a timestamp")
	}
}

// The file is named after the review, so a second store opened on the same path
// reads back the same pass — which is the whole point of keeping it there.
func TestReviewStoreReadsBackFromAnotherOpen(t *testing.T) {
	first := newTestReview(t)
	if _, err := first.Comments().Add(Comment{File: "pr.go", Line: 1, Body: "note"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	second := NewReviewStore(first.Path(), testTarget)
	comments, err := second.Comments().List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "note" {
		t.Errorf("comments = %v", comments)
	}
}

func TestReviewStoreUpdatesAndClears(t *testing.T) {
	s := newTestReview(t)
	kept, _ := s.Comments().Add(Comment{File: "pr.go", Line: 1, Body: "keep"})
	gone, _ := s.Comments().Add(Comment{File: "other.go", Line: 2, Body: "go"})

	if _, err := s.Comments().Update(kept.ID, func(c *Comment) { c.Resolved = true }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := s.Comments().Remove(gone.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	all, _ := s.Comments().List(Filter{})
	if len(all) != 1 || !all[0].Resolved {
		t.Fatalf("comments = %v", all)
	}

	n, err := s.Comments().Clear(Filter{})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if n != 1 {
		t.Errorf("cleared %d, want 1", n)
	}
	if left, _ := s.Comments().List(Filter{}); len(left) != 0 {
		t.Errorf("comments left after clearing: %v", left)
	}
}

func TestReviewStoreClearsTheWalkthroughAlone(t *testing.T) {
	s := newTestReview(t)
	if _, err := s.Comments().Add(Comment{File: "pr.go", Line: 1, Body: "note"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.Walkthroughs().Save(Walkthrough{Body: "## 1. It"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Walkthroughs().Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	if _, ok, _ := s.Walkthroughs().Load(); ok {
		t.Error("the narrative is still cached")
	}
	if all, _ := s.Comments().List(Filter{}); len(all) != 1 {
		t.Errorf("clearing the narrative took the notes with it: %v", all)
	}
}

// A review nobody has opened is empty rather than an error, and nothing is
// written until something is said about it.
func TestReviewStoreIsEmptyUntilWritten(t *testing.T) {
	s := newTestReview(t)

	all, err := s.Comments().List(Filter{})
	if err != nil || len(all) != 0 {
		t.Fatalf("List on a review nobody has read: %v, %v", all, err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Errorf("the file exists before anything was written: %v", err)
	}
}

// The file holds notes somebody wrote, so one that will not parse is reported
// rather than quietly opened as a blank review to write over.
func TestReviewStoreRefusesAFileItCannotRead(t *testing.T) {
	s := newTestReview(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(s.Path(), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := s.Comments().List(Filter{})
	if err == nil {
		t.Fatal("List read a corrupt review file as empty")
	}
	if !strings.Contains(err.Error(), s.Path()) {
		t.Errorf("error = %v, want it to name the file", err)
	}
}
