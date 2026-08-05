package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// openSearch presses the key the search opens on. cmd+p has no name in
// bubbletea, so what a test sends is the bytes a terminal writes for it, the
// same as a keyboard would.
func openSearch(t *testing.T, m *Model) {
	t.Helper()
	send(t, m, csiSequenceMsg("\x1b[112;9u"))
}

// hitPaths is what the search matches, best fit first, which is the order the
// panel lists them in.
func hitPaths(hits []findHit) []string {
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		out = append(out, hit.path)
	}
	return out
}

func TestGoingToAFileByName(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t,
		"internal/git/status.go", "internal/tui/view.go", "main.go")), WithSize(100, 14))

	openSearch(t, m)
	if m.mode != modeFind {
		t.Fatalf("mode = %v, want the file search", m.mode)
	}
	typeText(t, m, "view")
	press(t, m, "enter")

	if m.mode != modeBrowse {
		t.Fatalf("mode = %v, want browse once a file has been chosen", m.mode)
	}
	if got := m.markedPath(); got != "internal/tui/view.go" {
		t.Errorf("the window opened on %q, want internal/tui/view.go", got)
	}
	if got := m.currentPath(); got != "internal/tui/view.go" {
		t.Errorf("the cursor is in %q, want internal/tui/view.go", got)
	}
}

// The letters have to turn up in order but not next to each other, so a query
// can skip the directories a file is in as well as the middle of its name.
func TestTheSearchMatchesLettersInOrder(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "internal/tui/view.go", "main.go")))

	for _, query := range []string{"tuiview", "tvw", "VIEW", "view.go"} {
		m.find = finder{query: query}
		if got := hitPaths(m.findHits()); len(got) != 1 || got[0] != "internal/tui/view.go" {
			t.Errorf("%q matched %v, want internal/tui/view.go on its own", query, got)
		}
	}

	m.find = finder{query: "wiev"}
	if got := m.findHits(); len(got) != 0 {
		t.Errorf("letters out of order matched %v, want nothing", hitPaths(got))
	}
}

// A query that reads like the name of a file beats the same letters found
// scattered through a path that happens to hold them.
func TestTheSearchRanksANameOverThePathAroundIt(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t,
		"docs/preview/notes.md", "internal/tui/view.go", "vendor/i/e/w/other.go")))

	m.find = finder{query: "view"}
	want := []string{"internal/tui/view.go", "docs/preview/notes.md", "vendor/i/e/w/other.go"}
	if got := hitPaths(m.findHits()); !equal(got, want) {
		t.Errorf("the search lists\n%v\nwant\n%v", got, want)
	}
}

// Nothing typed yet lists every file in the order the diff reads them, with the
// choice on the file being read — so the search opens as the review it is part
// of rather than as an empty box.
func TestTheSearchOpensOnTheFileBeingRead(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "a.go", "b.go", "c.go")), WithSize(100, 14))

	press(t, m, "alt+down")
	openSearch(t, m)
	if got := hitPaths(m.findHits()); !equal(got, []string{"a.go", "b.go", "c.go"}) {
		t.Errorf("an empty query lists %v, want every file in the diff's order", got)
	}
	if got := m.findHits()[m.find.at].path; got != "b.go" {
		t.Errorf("the choice opened on %q, want the file being read, b.go", got)
	}
}

func TestEscLeavesTheSearchWithoutMoving(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "a.go", "b.go", "c.go")), WithSize(100, 14))

	before := m.cursor
	openSearch(t, m)
	typeText(t, m, "c")
	press(t, m, "esc")

	if m.mode != modeBrowse {
		t.Fatalf("mode = %v, want browse", m.mode)
	}
	if m.cursor != before {
		t.Errorf("esc left the cursor at %d, want it where it was at %d", m.cursor, before)
	}
	if m.find.query != "" {
		t.Errorf("the query %q survived the search being cancelled", m.find.query)
	}
}

// Backspace and ctrl+u widen the list again, and the choice goes back to the
// best match rather than staying on whatever row it was on.
func TestTheQueryIsEditedAsItIsTyped(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "alpha.go", "beta.go")), WithSize(100, 14))

	openSearch(t, m)
	typeText(t, m, "beta")
	if got := len(m.findHits()); got != 1 {
		t.Fatalf("%q matches %d files, want beta.go on its own", m.find.query, got)
	}

	press(t, m, "backspace")
	if m.find.query != "bet" {
		t.Errorf("query = %q, want bet", m.find.query)
	}
	press(t, m, "ctrl+u")
	if m.find.query != "" {
		t.Errorf("ctrl+u left %q, want the query cleared", m.find.query)
	}
	if got := len(m.findHits()); got != 2 {
		t.Errorf("the cleared query matches %d files, want both", got)
	}
}

// A query nothing matches goes nowhere: enter says so and leaves the review
// where it was, rather than jumping to whichever file happened to be first.
func TestGoingToAFileNothingMatchesSaysSo(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "a.go", "b.go")), WithSize(100, 14))

	before := m.cursor
	openSearch(t, m)
	typeText(t, m, "zzz")
	press(t, m, "enter")

	if m.cursor != before {
		t.Errorf("the cursor moved to %d on a query nothing matches", m.cursor)
	}
	if !strings.Contains(m.status, "no file matches") {
		t.Errorf("status = %q, want it to say nothing matched", m.status)
	}
}

// A file folded away is gone to and left folded — the fold says it was dealt
// with — and the footer says which key opens it.
func TestGoingToAFoldedFileSaysItIsFolded(t *testing.T) {
	backend := newFakeBackend(pathSession(t, "alpha.go", "beta.go"))
	m := newModel(t, backend, WithSize(100, 14))

	press(t, m, "space")
	if !m.collapsed["alpha.go"] {
		t.Fatal("space did not fold alpha.go away")
	}

	openSearch(t, m)
	typeText(t, m, "alpha")
	press(t, m, "enter")

	if !m.collapsed["alpha.go"] {
		t.Error("going to alpha.go opened it again, want the fold left alone")
	}
	if m.currentPath() != "alpha.go" {
		t.Errorf("the cursor is in %q, want alpha.go", m.currentPath())
	}
	if !strings.Contains(m.status, "folded") {
		t.Errorf("status = %q, want it to say the file is folded away", m.status)
	}
}

// The search is drawn over the foot of the body, so the diff it is being run
// against is still on screen above it.
func TestTheSearchIsDrawnOverTheFootOfTheDiff(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "alpha.go", "beta.go")), WithSize(60, 20))

	openSearch(t, m)
	typeText(t, m, "beta")
	lines := strings.Split(m.View(), "\n")

	if !strings.Contains(m.View(), "go to file") {
		t.Fatal("the search does not name itself")
	}
	if !strings.Contains(m.View(), "beta.go") {
		t.Error("the search does not list what the query matches")
	}
	if !strings.Contains(strings.Join(lines[:len(lines)/2], "\n"), "alpha.go") {
		t.Error("the diff above the search has gone")
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != 60 {
			t.Fatalf("row %d is %d columns wide, want 60", i, got)
		}
	}
}

// The panel lists what it has room for and scrolls, so the choice is on screen
// however far down the list it is moved.
func TestTheSearchScrollsToTheChoice(t *testing.T) {
	m := newModel(t, newFakeBackend(manyFileSession(t, 30)), WithSize(100, 20))

	openSearch(t, m)
	for range 20 {
		press(t, m, "down")
	}
	if m.find.at != 20 {
		t.Fatalf("the choice is on match %d, want the twenty-first", m.find.at)
	}
	if m.find.at < m.find.top || m.find.at >= m.find.top+m.findHeight() {
		t.Errorf("the choice at %d is outside the window from %d", m.find.at, m.find.top)
	}
	if !strings.Contains(m.View(), "f20.txt") {
		t.Error("the file the choice is on is not on screen")
	}

	// It stops at the last match rather than running off the end of the list.
	for range 30 {
		press(t, m, "down")
	}
	if m.find.at != 29 {
		t.Errorf("the choice ran to %d, want it stopped on the last of 30 files", m.find.at)
	}
}

// cmd+p opens the search wherever the terminal reports the modifier, in either
// of the two forms one writes it in. bubbletea has no name for the sequence, so
// what peel reads is the raw bytes — the same path cmd+↑ and cmd+↓ take.
func TestCmdPOpensTheSearch(t *testing.T) {
	for _, seq := range []string{"\x1b[112;9u", "\x1b[112;9:1u", "\x1b[112;33u", "\x1b[27;9;112~"} {
		m := newModel(t, newFakeBackend(pathSession(t, "a.go", "b.go")), WithSize(100, 14))

		send(t, m, csiSequenceMsg(seq))
		if m.mode != modeFind {
			t.Errorf("%q left the mode at %v, want the file search", seq, m.mode)
		}
	}
}

// A letter reported for some other modifier is a press meant for something
// else, and so is cmd on a letter peel does not bind.
func TestModifiedLettersWithoutCmdAreLeftAlone(t *testing.T) {
	for _, seq := range []string{"\x1b[112;5u", "\x1b[112;2u", "\x1b[113;9u", "\x1b[27;5;112~", "\x1b[112;u"} {
		m := newModel(t, newFakeBackend(pathSession(t, "a.go", "b.go")), WithSize(100, 14))

		send(t, m, csiSequenceMsg(seq))
		if m.mode != modeBrowse {
			t.Errorf("%q opened %v, want the press left alone", seq, m.mode)
		}
	}
}

// And the bytes reach it through bubbletea's own input loop, not only through a
// message a test made up.
func TestCmdPBytesOpenTheSearch(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "a.go", "b.go")), WithSize(100, 14))

	if final := runBytes(t, m, "\x1b[112;9u"); final.mode != modeFind {
		t.Errorf("mode = %v, want the file search", final.mode)
	}
}

// cmd is the modifier the search opens on and the only one: ctrl+p is a press
// peel says nothing about, so a terminal that sends it reaches nothing.
func TestCtrlPDoesNotOpenTheSearch(t *testing.T) {
	m := newModel(t, newFakeBackend(pathSession(t, "a.go", "b.go")), WithSize(100, 14))

	press(t, m, "ctrl+p")
	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want ctrl+p left alone", m.mode)
	}
}
