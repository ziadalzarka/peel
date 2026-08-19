package tui

import (
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/store"
)

// outdatedNote is a note whose code was rewritten out from under it.
func outdatedNote() store.Comment {
	return store.Comment{
		ID: "c1", File: "svc.go", Line: 42, Side: store.SideNew,
		Body: "this leaks the tx", Author: store.AuthorUser, Outdated: true,
	}
}

// TestAnOutdatedNoteCarriesItsOwnExplanation checks the tag says what happened.
// Parked under a file saying nothing, the note gives a reader no way to tell
// what it was ever about.
func TestAnOutdatedNoteCarriesItsOwnExplanation(t *testing.T) {
	tag := commentTag(outdatedNote())
	if !strings.Contains(tag, "outdated") {
		t.Errorf("tag = %q, want it to say the note is outdated", tag)
	}
	if !strings.Contains(tag, "42") {
		t.Errorf("tag = %q, want the line it was written on", tag)
	}
}

// TestAResolvedNoteStillReadsAsResolvedWhenOutdated keeps the two marks from
// displacing each other: a note can be both, and the ✓ is the one the reviewer
// scans for.
func TestAResolvedNoteStillReadsAsResolvedWhenOutdated(t *testing.T) {
	c := outdatedNote()
	c.Resolved = true
	tag := commentTag(c)
	if !strings.Contains(tag, "✓") || !strings.Contains(tag, "outdated") {
		t.Errorf("tag = %q, want both the resolved mark and the outdated note", tag)
	}
}

// TestACurrentNoteSaysNothingExtra guards against tagging every note. The mark
// only means something if it is absent when the note is fine.
func TestACurrentNoteSaysNothingExtra(t *testing.T) {
	c := outdatedNote()
	c.Outdated = false
	if tag := commentTag(c); strings.Contains(tag, "outdated") {
		t.Errorf("tag = %q, want no mark on a note that is where it says", tag)
	}
}

// TestTheHandoffWarnsAboutAnOutdatedLine covers the paste path: an agent that
// cannot read the store is handed the notes as text, and a line number it must
// not take at face value has to say so there too.
func TestTheHandoffWarnsAboutAnOutdatedLine(t *testing.T) {
	got := commentHandoff([]store.Comment{outdatedNote()}, nil)
	if !strings.Contains(got, "svc.go:42") {
		t.Errorf("the handoff lost the anchor:\n%s", got)
	}
	if !strings.Contains(got, "since changed") {
		t.Errorf("the handoff sends an agent to a line that has been rewritten:\n%s", got)
	}
}

// TestTheHandoffLeavesACurrentLinePlain keeps the warning meaningful.
func TestTheHandoffLeavesACurrentLinePlain(t *testing.T) {
	c := outdatedNote()
	c.Outdated = false
	got := commentHandoff([]store.Comment{c}, nil)
	if strings.Contains(got, "since changed") {
		t.Errorf("a note that is where it says was warned about:\n%s", got)
	}
}
