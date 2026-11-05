package tui

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/store"
)

func jumpModel(t *testing.T, runs ...store.HiddenRun) (*Model, *fakeBackend) {
	t.Helper()
	s := newSession(t, casesDiff)
	f := newFakeBackend(s)
	f.runs = runs
	return New(context.Background(), f, s, nil, WithoutSyntax(), WithSize(100, 12)), f
}

func newLineUnderCursor(t *testing.T, m *Model) int {
	t.Helper()
	r := m.doc.Rows[m.cursor]
	if r.Kind != RowLine {
		t.Fatalf("cursor is on row kind %v, not a line", r.Kind)
	}
	lines := m.doc.Hunks[r.Hunk].Hunk.Lines
	for _, at := range []int{r.Right, r.Left} {
		if at >= 0 && lines[at].NewLine > 0 {
			return lines[at].NewLine
		}
	}
	t.Fatal("the cursor line has no new line number")
	return 0
}

func TestGoToMovesTheCursorToTheLine(t *testing.T) {
	m, _ := jumpModel(t)
	m.goTo("cases_test.go", 8)
	if got := newLineUnderCursor(t, m); got != 8 {
		t.Errorf("cursor is on line %d, want 8", got)
	}
}

func TestGoToOpensTheFileWhenItIsFolded(t *testing.T) {
	m, _ := jumpModel(t)
	m.setCollapsed(map[string]bool{"cases_test.go": true})
	m.rebuild()
	m.goTo("cases_test.go", 8)
	if m.collapsed["cases_test.go"] {
		t.Error("the file stayed folded")
	}
	if got := newLineUnderCursor(t, m); got != 8 {
		t.Errorf("cursor is on line %d, want 8", got)
	}
}

func TestGoToOpensTheRunHidingTheLine(t *testing.T) {
	run := bodiesRun()
	m, _ := jumpModel(t, run)
	m.goTo("cases_test.go", run.Line)
	if !m.openRuns[run.ID] {
		t.Error("the run hiding the line stayed closed")
	}
	if got := newLineUnderCursor(t, m); got != run.Line {
		t.Errorf("cursor is on line %d, want %d", got, run.Line)
	}
}

func TestGoToOpensAFileHiddenAsAWhole(t *testing.T) {
	run := store.HiddenRun{ID: "whole", File: "cases_test.go", Author: "claude"}
	m, _ := jumpModel(t, run)
	m.goTo("cases_test.go", 8)
	if got := newLineUnderCursor(t, m); got != 8 {
		t.Errorf("cursor is on line %d, want 8", got)
	}
}

func TestGoToALineOutsideTheDiffStopsOnTheNearestOne(t *testing.T) {
	m, _ := jumpModel(t)
	m.goTo("cases_test.go", 500)
	if got := newLineUnderCursor(t, m); got != 10 {
		t.Errorf("cursor is on line %d, want the last line in the diff, 10", got)
	}
}

func TestGoToAFileOutsideTheDiffSaysSo(t *testing.T) {
	m, _ := jumpModel(t)
	before := m.cursor
	m.goTo("other.go", 1)
	if m.cursor != before {
		t.Error("the cursor moved")
	}
	if m.status != "other.go is not in this diff" {
		t.Errorf("status = %q", m.status)
	}
}

func TestAJumpAskedForAfterOpeningIsPickedUp(t *testing.T) {
	m, f := jumpModel(t)
	f.jump = store.Jump{File: "cases_test.go", Line: 8, At: time.Now()}
	got := m.jumpCmd()()
	msg, ok := got.(jumpMsg)
	if !ok || msg.Line != 8 {
		t.Fatalf("jumpCmd returned %#v", got)
	}
	next, cmd := m.Update(msg)
	if cmd == nil {
		t.Error("the model stopped watching for jumps")
	}
	if line := newLineUnderCursor(t, next.(*Model)); line != 8 {
		t.Errorf("cursor is on line %d, want 8", line)
	}
}

func TestAnOldJumpIsNotPickedUp(t *testing.T) {
	m, f := jumpModel(t)
	f.jump = store.Jump{File: "cases_test.go", Line: 8, At: time.Now().Add(-2 * jumpMaxAge)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.ctx = ctx
	if got := m.jumpCmd()(); got != nil {
		t.Errorf("jumpCmd returned %#v, want nothing", got)
	}
}

func TestReviewModeCollapsesRunsOnAPullRequest(t *testing.T) {
	s := newSession(t, casesDiff)
	s.PR = &forge.PullRequest{Ref: forge.Ref{Owner: "o", Repo: "r", Number: 1}, HeadSHA: "abc"}
	s.Stageable = true
	f := newFakeBackend(s)
	f.runs = []store.HiddenRun{bodiesRun()}
	var m tea.Model = New(context.Background(), f, s, nil, WithoutSyntax(), WithSize(100, 30))
	model := m.(*Model)
	if model.doc.HiddenLines != 2 {
		t.Fatalf("HiddenLines = %d, want 2", model.doc.HiddenLines)
	}
	model.toggleOthers()
	if model.doc.HiddenLines != 0 {
		t.Errorf("HiddenLines with review mode off = %d, want 0", model.doc.HiddenLines)
	}
}

func TestPendingReviewLeavesOutAnAgentsNotes(t *testing.T) {
	s := prSession(t)
	f := newFakeBackend(s)
	comments := []store.Comment{
		{ID: "a", File: "pr.go", Line: 1, Body: "mine", Author: "ziad"},
		{ID: "b", File: "pr.go", Line: 1, Body: "agent", Author: "claude"},
	}
	m := New(context.Background(), f, s, comments, WithoutSyntax(), WithAuthor("ziad"))
	if inline, _ := m.pendingReview(); inline != 1 {
		t.Errorf("pending inline notes = %d, want 1", inline)
	}
}
