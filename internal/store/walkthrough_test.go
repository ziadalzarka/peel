package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWalkthroughSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peel", "walkthrough.json")
	c := NewJSONWalkthroughCache(path)

	want := Walkthrough{
		Target:      "",
		Fingerprint: "abc123",
		Provider:    "claude-code",
		Body:        "## What changed\n\nThis branch moves document keys off the model.",
	}
	if err := c.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !ok {
		t.Fatal("Load reported nothing cached")
	}
	if got.Body != want.Body || got.Fingerprint != want.Fingerprint || got.Provider != want.Provider {
		t.Errorf("Load returned %+v, want %+v", got, want)
	}
	if got.CreatedAt.IsZero() {
		t.Error("Save did not set CreatedAt")
	}
}

func TestWalkthroughLoadMissing(t *testing.T) {
	c := NewJSONWalkthroughCache(filepath.Join(t.TempDir(), "walkthrough.json"))
	_, ok, err := c.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Error("Load reported a cache hit on a missing file")
	}
}

func TestWalkthroughCorruptCacheIsIgnored(t *testing.T) {
	// A corrupt cache should regenerate silently, not fail the command.
	path := filepath.Join(t.TempDir(), "walkthrough.json")
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, ok, err := NewJSONWalkthroughCache(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ok {
		t.Error("Load reported a hit on a corrupt file")
	}
}

func TestWalkthroughFresh(t *testing.T) {
	w := Walkthrough{Target: "github:o/r#7", Fingerprint: "abc", Body: "text"}

	tests := []struct {
		name        string
		target      string
		fingerprint string
		want        bool
	}{
		{"same target and diff", "github:o/r#7", "abc", true},
		{"diff moved on", "github:o/r#7", "def", false},
		{"different target", "", "abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := w.Fresh(tt.target, tt.fingerprint); got != tt.want {
				t.Errorf("Fresh() = %v, want %v", got, tt.want)
			}
		})
	}

	empty := Walkthrough{Target: "", Fingerprint: "abc"}
	if empty.Fresh("", "abc") {
		t.Error("an empty body counted as fresh")
	}
}

func TestWalkthroughClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walkthrough.json")
	c := NewJSONWalkthroughCache(path)
	if err := c.Save(Walkthrough{Body: "text", Fingerprint: "x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := c.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, ok, _ := c.Load(); ok {
		t.Error("Load reported a hit after Clear")
	}
	// Clearing an already-absent cache is not an error.
	if err := c.Clear(); err != nil {
		t.Errorf("Clear on a missing file: %v", err)
	}
}

func TestWalkthroughOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "walkthrough.json")
	c := NewJSONWalkthroughCache(path, WithWalkthroughClock(func() time.Time {
		return time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC)
	}))

	if err := c.Save(Walkthrough{Body: "first", Fingerprint: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := c.Save(Walkthrough{Body: "second", Fingerprint: "b"}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok, err := c.Load()
	if err != nil || !ok {
		t.Fatalf("Load: %v ok=%v", err, ok)
	}
	if got.Body != "second" {
		t.Errorf("Body = %q, want the most recent narrative", got.Body)
	}
}

func TestFingerprintChangesWithDiff(t *testing.T) {
	a := Fingerprint("diff --git a/f b/f\n+one\n")
	b := Fingerprint("diff --git a/f b/f\n+two\n")
	if a == b {
		t.Error("different diffs produced the same fingerprint")
	}
	if a != Fingerprint("diff --git a/f b/f\n+one\n") {
		t.Error("Fingerprint is not deterministic")
	}
	if a == "" {
		t.Error("Fingerprint returned an empty string")
	}
}

var _ WalkthroughCache = (*JSONWalkthroughCache)(nil)
