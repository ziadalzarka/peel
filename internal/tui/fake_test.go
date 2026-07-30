package tui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// twoFileDiff has one file with a replacement plus an addition, and one with a
// single replaced line.
const twoFileDiff = `diff --git a/alpha.go b/alpha.go
index 1111111..2222222 100644
--- a/alpha.go
+++ b/alpha.go
@@ -1,4 +1,5 @@ package alpha
 package alpha

-func One() int { return 1 }
+func One() int { return 2 }
+func Two() int { return 2 }

diff --git a/beta.txt b/beta.txt
index 3333333..4444444 100644
--- a/beta.txt
+++ b/beta.txt
@@ -1,2 +1,2 @@
 keep
-old
+new
`

// threeFileDiff is twoFileDiff with a third file after it, for the moves that
// need somewhere to land past the file in the middle.
const threeFileDiff = twoFileDiff + `diff --git a/gamma.md b/gamma.md
index 5555555..6666666 100644
--- a/gamma.md
+++ b/gamma.md
@@ -1,2 +1,2 @@
 # notes
-first
+second
`

// wideLine is far wider than any pane the tests open, so reading its tail means
// scrolling sideways.
var wideLine = "var long = " + strconv.Quote(strings.Repeat("0123456789", 12))

// longLineDiff replaces a short line with a very long one, which puts both on
// the same row in the split layout.
var longLineDiff = "diff --git a/wide.go b/wide.go\n" +
	"index 1111111..2222222 100644\n--- a/wide.go\n+++ b/wide.go\n" +
	"@@ -1,2 +1,2 @@\n" +
	" package wide\n" +
	"-var short = 1\n" +
	"+" + wideLine + "\n"

// parseFiles turns a diff into file entries on the working-tree side.
func parseFiles(t *testing.T, diff string) []git.FileEntry {
	t.Helper()
	parsed, err := git.ParseDiff(diff)
	if err != nil {
		t.Fatalf("ParseDiff: %v", err)
	}
	entries := make([]git.FileEntry, 0, len(parsed.Files))
	for i := range parsed.Files {
		f := parsed.Files[i]
		entries = append(entries, git.FileEntry{Path: f.Path(), Unstaged: &f})
	}
	return entries
}

// newSession builds a stageable working-tree session from a diff.
func newSession(t *testing.T, diff string) *app.Session {
	t.Helper()
	return sessionOf(parseFiles(t, diff))
}

// sessionOf wraps prepared file entries in a stageable session.
func sessionOf(entries []git.FileEntry) *app.Session {
	return &app.Session{Title: "working tree", Files: entries, Stageable: true}
}

// groupedWalkthrough is the shape the default instruction asks a provider for:
// a numbered step, the files it covers, then the explanation.
const groupedWalkthrough = "## 1. The function alpha exports\n" +
	"`alpha.go`\n" +
	"\n" +
	"`One` returns 2 now, and `Two` arrives beside it.\n" +
	"\n" +
	"## 2. The fixture that follows it\n" +
	"`beta.txt`\n" +
	"\n" +
	"The single line moves from old to new.\n"

// fakeBackend records every call so tests can assert on what the UI asked for
// rather than on what git did with it.
type fakeBackend struct {
	session  *app.Session
	comments []store.Comment

	stagedFiles   []string
	unstagedFiles []string
	stageAll      int
	unstageAll    int
	opened        []string
	// copied is every text handed to the clipboard, in order.
	copied []string

	// folded is what the review folded away, kept the way the store would.
	folded      []string
	foldSaves   int
	foldErr     error
	foldSaveErr error

	added    []store.Comment
	removed  []string
	resolved map[string]bool

	walkCalls  int
	regenerate bool
	walkBody   string

	// opErr fails the next mutation.
	opErr error
	// reloadErr fails Reload.
	reloadErr error
	// nextSession replaces the session on the next Reload.
	nextSession *app.Session
	// nextComments replaces the comments on the next Comments call.
	nextComments []store.Comment

	reloads int
	nextID  int
}

func newFakeBackend(s *app.Session) *fakeBackend {
	return &fakeBackend{session: s, resolved: map[string]bool{}, walkBody: groupedWalkthrough}
}

func (f *fakeBackend) Reload(context.Context) (*app.Session, error) {
	f.reloads++
	if f.reloadErr != nil {
		return nil, f.reloadErr
	}
	if f.nextSession != nil {
		f.session = f.nextSession
		f.nextSession = nil
	}
	return f.session, nil
}

func (f *fakeBackend) Comments() ([]store.Comment, error) {
	if f.nextComments != nil {
		f.comments = f.nextComments
		f.nextComments = nil
	}
	return f.comments, nil
}

func (f *fakeBackend) AddComment(c store.Comment) (store.Comment, error) {
	if err := f.take(); err != nil {
		return store.Comment{}, err
	}
	f.nextID++
	c.ID = fmt.Sprintf("c%d", f.nextID)
	c.CreatedAt = time.Unix(int64(f.nextID), 0).UTC()
	f.added = append(f.added, c)
	f.comments = append(f.comments, c)
	return c, nil
}

func (f *fakeBackend) RemoveComment(id string) error {
	if err := f.take(); err != nil {
		return err
	}
	f.removed = append(f.removed, id)
	kept := f.comments[:0]
	for _, c := range f.comments {
		if c.ID != id {
			kept = append(kept, c)
		}
	}
	f.comments = kept
	return nil
}

func (f *fakeBackend) SetResolved(id string, resolved bool) error {
	if err := f.take(); err != nil {
		return err
	}
	f.resolved[id] = resolved
	for i := range f.comments {
		if f.comments[i].ID == id {
			f.comments[i].Resolved = resolved
		}
	}
	return nil
}

func (f *fakeBackend) StageFile(_ context.Context, path string) error {
	if err := f.take(); err != nil {
		return err
	}
	f.stagedFiles = append(f.stagedFiles, path)
	return nil
}

func (f *fakeBackend) UnstageFile(_ context.Context, path string) error {
	if err := f.take(); err != nil {
		return err
	}
	f.unstagedFiles = append(f.unstagedFiles, path)
	return nil
}

func (f *fakeBackend) StageAll(context.Context) error {
	if err := f.take(); err != nil {
		return err
	}
	f.stageAll++
	return nil
}

func (f *fakeBackend) UnstageAll(context.Context) error {
	if err := f.take(); err != nil {
		return err
	}
	f.unstageAll++
	return nil
}

func (f *fakeBackend) Folded() ([]string, error) {
	if f.foldErr != nil {
		return nil, f.foldErr
	}
	return f.folded, nil
}

func (f *fakeBackend) SetFolded(paths []string) error {
	sort.Strings(paths)
	f.folded = paths
	f.foldSaves++
	return f.foldSaveErr
}

func (f *fakeBackend) OpenFile(_ context.Context, path string) error {
	if err := f.take(); err != nil {
		return err
	}
	f.opened = append(f.opened, path)
	return nil
}

func (f *fakeBackend) Copy(_ context.Context, text string) error {
	if err := f.take(); err != nil {
		return err
	}
	f.copied = append(f.copied, text)
	return nil
}

func (f *fakeBackend) Walkthrough(_ context.Context, regenerate bool) (string, error) {
	f.walkCalls++
	f.regenerate = regenerate
	if err := f.take(); err != nil {
		return "", err
	}
	return f.walkBody, nil
}

// take consumes a one-shot error, so a test can fail exactly one operation.
func (f *fakeBackend) take() error {
	err := f.opErr
	f.opErr = nil
	return err
}
