package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestStore returns a store with deterministic IDs and clock.
func newTestStore(t *testing.T) *JSONStore {
	t.Helper()
	var n int
	var mu sync.Mutex
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

	return NewJSONStore(filepath.Join(t.TempDir(), "peel", "comments.json"),
		WithIDGenerator(func() string {
			mu.Lock()
			defer mu.Unlock()
			n++
			return "id" + string(rune('0'+n))
		}),
		WithClock(func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return base.Add(time.Duration(n) * time.Minute)
		}),
	)
}

func TestAddAndList(t *testing.T) {
	s := newTestStore(t)

	got, err := s.Add(Comment{File: "src/main.go", Line: 42, Body: "this leaks the tx"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got.ID == "" {
		t.Error("Add did not assign an ID")
	}
	if got.Side != SideNew {
		t.Errorf("Side = %q, want new by default", got.Side)
	}
	if got.Author != AuthorUser {
		t.Errorf("Author = %q, want user by default", got.Author)
	}
	if got.CreatedAt.IsZero() {
		t.Error("Add did not set CreatedAt")
	}

	all, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d comments, want 1", len(all))
	}
	if all[0].Body != "this leaks the tx" {
		t.Errorf("Body = %q", all[0].Body)
	}
}

func TestListEmptyStore(t *testing.T) {
	s := newTestStore(t)
	all, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List on a missing file: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("got %d comments, want 0", len(all))
	}
}

func TestAddValidation(t *testing.T) {
	tests := []struct {
		name string
		c    Comment
	}{
		{"no file", Comment{Body: "x"}},
		{"blank file", Comment{File: "   ", Body: "x"}},
		{"no body", Comment{File: "f.go"}},
		{"blank body", Comment{File: "f.go", Body: "  "}},
		{"negative line", Comment{File: "f.go", Body: "x", Line: -1}},
		{"bad side", Comment{File: "f.go", Body: "x", Side: "sideways"}},
		{"bad author", Comment{File: "f.go", Body: "x", Author: "robot"}},
	}

	s := newTestStore(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := s.Add(tt.c); err == nil {
				t.Fatal("Add succeeded, want a validation error")
			}
		})
	}
}

func TestAddRejectsDuplicateID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Add(Comment{ID: "fixed", File: "f.go", Body: "one"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := s.Add(Comment{ID: "fixed", File: "f.go", Body: "two"}); err == nil {
		t.Fatal("Add accepted a duplicate ID")
	}
}

func TestPersistsAcrossInstances(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peel", "comments.json")

	first := NewJSONStore(path)
	if _, err := first.Add(Comment{File: "f.go", Line: 1, Body: "survives"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// A separate process reads the same file — this is how the agent sees the
	// user's notes after the TUI has exited.
	second := NewJSONStore(path)
	all, err := second.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].Body != "survives" {
		t.Errorf("got %+v, want the comment written by the first instance", all)
	}
}

func TestFilters(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Comment{File: "a.go", Line: 1, Body: "one"})
	mustAdd(t, s, Comment{File: "b.go", Line: 2, Body: "two", Author: AuthorAgent})
	resolved := mustAdd(t, s, Comment{File: "a.go", Line: 3, Body: "three"})
	mustAdd(t, s, Comment{File: "a.go", Line: 4, Body: "four", Target: "github:o/r#7"})

	if _, err := s.Update(resolved.ID, func(c *Comment) { c.Resolved = true }); err != nil {
		t.Fatalf("Update: %v", err)
	}

	tests := []struct {
		name string
		f    Filter
		want int
	}{
		{"all", Filter{}, 4},
		{"by file", Filter{File: "a.go"}, 3},
		{"unresolved", Filter{Unresolved: true}, 3},
		{"file and unresolved", Filter{File: "a.go", Unresolved: true}, 2},
		{"by author", Filter{Author: AuthorAgent}, 1},
		{"by target", Filter{Target: "github:o/r#7", MatchTarget: true}, 1},
		{"working tree only", Filter{Target: "", MatchTarget: true}, 3},
		{"no match", Filter{File: "nope.go"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.List(tt.f)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(got) != tt.want {
				t.Errorf("got %d comments, want %d", len(got), tt.want)
			}
		})
	}
}

func TestListIsOrderedOldestFirst(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustAdd(t, s, Comment{File: "f.go", Body: "third", CreatedAt: base.Add(2 * time.Hour)})
	mustAdd(t, s, Comment{File: "f.go", Body: "first", CreatedAt: base})
	mustAdd(t, s, Comment{File: "f.go", Body: "second", CreatedAt: base.Add(time.Hour)})

	all, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"first", "second", "third"}
	for i, w := range want {
		if all[i].Body != w {
			t.Errorf("position %d = %q, want %q", i, all[i].Body, w)
		}
	}
}

func TestUpdate(t *testing.T) {
	s := newTestStore(t)
	c := mustAdd(t, s, Comment{File: "f.go", Line: 5, Body: "original"})

	got, err := s.Update(c.ID, func(c *Comment) {
		c.Body = "revised"
		c.Resolved = true
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.Body != "revised" || !got.Resolved {
		t.Errorf("Update returned %+v", got)
	}

	reread, err := s.Get(c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reread.Body != "revised" {
		t.Errorf("change did not persist: %+v", reread)
	}
}

func TestUpdateCannotChangeID(t *testing.T) {
	s := newTestStore(t)
	c := mustAdd(t, s, Comment{File: "f.go", Body: "x"})

	got, err := s.Update(c.ID, func(c *Comment) { c.ID = "hijacked" })
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.ID != c.ID {
		t.Errorf("ID = %q, want it unchanged at %q", got.ID, c.ID)
	}
}

func TestUpdateRejectsInvalidResult(t *testing.T) {
	s := newTestStore(t)
	c := mustAdd(t, s, Comment{File: "f.go", Body: "x"})

	if _, err := s.Update(c.ID, func(c *Comment) { c.Body = "" }); err == nil {
		t.Fatal("Update accepted an empty body")
	}
	// The stored comment must be untouched after a rejected update.
	reread, err := s.Get(c.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reread.Body != "x" {
		t.Errorf("Body = %q, want the original preserved", reread.Body)
	}
}

func TestUpdateMissingID(t *testing.T) {
	s := newTestStore(t)
	_, err := s.Update("nope", func(*Comment) {})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestRemove(t *testing.T) {
	s := newTestStore(t)
	a := mustAdd(t, s, Comment{File: "f.go", Body: "keep"})
	b := mustAdd(t, s, Comment{File: "f.go", Body: "drop"})

	if err := s.Remove(b.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	all, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].ID != a.ID {
		t.Errorf("got %+v, want only the kept comment", all)
	}
}

func TestRemoveMissingID(t *testing.T) {
	s := newTestStore(t)
	if err := s.Remove("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

func TestClear(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Comment{File: "a.go", Body: "one"})
	mustAdd(t, s, Comment{File: "a.go", Body: "two"})
	mustAdd(t, s, Comment{File: "b.go", Body: "three"})

	n, err := s.Clear(Filter{File: "a.go"})
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if n != 2 {
		t.Errorf("Clear removed %d, want 2", n)
	}

	all, _ := s.List(Filter{})
	if len(all) != 1 || all[0].File != "b.go" {
		t.Errorf("got %+v, want only b.go", all)
	}
}

func TestClearEverything(t *testing.T) {
	s := newTestStore(t)
	mustAdd(t, s, Comment{File: "a.go", Body: "one"})
	mustAdd(t, s, Comment{File: "b.go", Body: "two"})

	if _, err := s.Clear(Filter{}); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	all, _ := s.List(Filter{})
	if len(all) != 0 {
		t.Errorf("got %d comments, want 0", len(all))
	}
}

func TestConcurrentAddsAllSurvive(t *testing.T) {
	// The TUI and an agent can write at the same time; the lock must stop one
	// from overwriting the other's read-modify-write.
	s := newTestStore(t)
	const n = 20

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Add(Comment{File: "f.go", Line: i + 1, Body: "note"})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Add: %v", err)
	}

	all, err := s.List(Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != n {
		t.Errorf("got %d comments, want %d — a concurrent write was lost", len(all), n)
	}

	seen := map[string]bool{}
	for _, c := range all {
		if seen[c.ID] {
			t.Errorf("duplicate ID %q", c.ID)
		}
		seen[c.ID] = true
	}
}

func TestWriteIsAtomic(t *testing.T) {
	// A rename-based write leaves no temp files behind.
	dir := t.TempDir()
	s := NewJSONStore(filepath.Join(dir, "comments.json"))
	mustAdd(t, s, Comment{File: "f.go", Body: "x"})

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".comments-") {
			t.Errorf("temp file %s left behind", e.Name())
		}
	}
}

func TestStaleLockIsReclaimed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comments.json")
	s := NewJSONStore(path, WithLockTimeout(100*time.Millisecond))
	s.lockStale = 10 * time.Millisecond

	// A lock left by a killed process must not block writes forever.
	if err := os.WriteFile(path+".lock", nil, 0o644); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	if _, err := s.Add(Comment{File: "f.go", Body: "x"}); err != nil {
		t.Fatalf("Add did not reclaim a stale lock: %v", err)
	}
}

func TestLockTimeoutIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comments.json")
	s := NewJSONStore(path, WithLockTimeout(30*time.Millisecond))

	if err := os.WriteFile(path+".lock", nil, 0o644); err != nil {
		t.Fatalf("write lock file: %v", err)
	}

	_, err := s.Add(Comment{File: "f.go", Body: "x"})
	if err == nil {
		t.Fatal("Add succeeded despite a held lock")
	}
	if !strings.Contains(err.Error(), "lock") {
		t.Errorf("error = %v, want it to name the lock", err)
	}
}

func TestRejectsNewerFormatVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comments.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"comments":[]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := NewJSONStore(path)
	_, err := s.List(Filter{})
	if err == nil {
		t.Fatal("List accepted a newer format version")
	}
	if !strings.Contains(err.Error(), "newer peel") {
		t.Errorf("error = %v, want it to explain the version mismatch", err)
	}
}

func TestRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comments.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := NewJSONStore(path).List(Filter{}); err == nil {
		t.Fatal("List accepted a corrupt file")
	}
}

func TestCommentLocation(t *testing.T) {
	tests := []struct {
		c    Comment
		want string
	}{
		{Comment{File: "src/main.go", Line: 42}, "src/main.go:42"},
		{Comment{File: "src/main.go", Line: 0}, "src/main.go"},
	}
	for _, tt := range tests {
		if got := tt.c.Location(); got != tt.want {
			t.Errorf("Location() = %q, want %q", got, tt.want)
		}
	}
}

// JSONStore must satisfy the interface the rest of peel depends on.
var _ CommentStore = (*JSONStore)(nil)

func mustAdd(t *testing.T, s *JSONStore, c Comment) Comment {
	t.Helper()
	got, err := s.Add(c)
	if err != nil {
		t.Fatalf("Add(%+v): %v", c, err)
	}
	return got
}
