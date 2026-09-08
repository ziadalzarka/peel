package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// screenRows is what is on screen as plain text, one string per row.
func screenRows(t *testing.T, m *Model) []string {
	t.Helper()
	return strings.Split(ansi.Strip(m.View()), "\n")
}

// swept is the text of one row between two columns, sliced without the code
// under test: the model is drawn plain in these tests, so a rune slice is the
// whole story.
func swept(t *testing.T, m *Model, row, lo, hi int) string {
	t.Helper()
	runes := []rune(screenRows(t, m)[row])
	return strings.TrimRight(string(runes[min(lo, len(runes)):min(hi, len(runes))]), " ")
}

// syntaxModel builds a Model the way the program does, colours and all, for the
// tests that need real escape sequences on the rows being swept over.
func syntaxModel(t *testing.T, backend *fakeBackend) *Model {
	t.Helper()
	comments, err := backend.Comments(t.Context())
	if err != nil {
		t.Fatalf("Comments: %v", err)
	}
	return New(context.Background(), backend, backend.session, comments, WithSize(100, 30))
}

func mouseAt(action tea.MouseAction, at cell) tea.MouseMsg {
	return tea.MouseMsg{X: at.col, Y: at.row, Action: action, Button: tea.MouseButtonLeft}
}

// dragOver sweeps the pointer from one cell to another, drawing between the
// events the way the program does.
func dragOver(t *testing.T, m *Model, from, to cell) {
	t.Helper()
	m.View()
	send(t, m, mouseAt(tea.MouseActionPress, from))
	m.View()
	send(t, m, mouseAt(tea.MouseActionMotion, to))
	m.View()
	send(t, m, mouseAt(tea.MouseActionRelease, to))
}

func TestDraggingAcrossARowCopiesWhatWasSweptOver(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	want := swept(t, m, 5, 26, 61)
	dragOver(t, m, cell{row: 5, col: 26}, cell{row: 5, col: 60})

	if len(backend.copied) != 1 {
		t.Fatalf("Copy called %d times, want 1", len(backend.copied))
	}
	if backend.copied[0] != want {
		t.Errorf("copied %q, want %q", backend.copied[0], want)
	}
	if !strings.Contains(m.status, "copied 1 line") {
		t.Errorf("status = %q, want it to say one line was copied", m.status)
	}
}

func TestDraggingBackwardsCopiesTheSameText(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	want := swept(t, m, 5, 26, 61)
	dragOver(t, m, cell{row: 5, col: 60}, cell{row: 5, col: 26})

	if len(backend.copied) != 1 {
		t.Fatalf("Copy called %d times, want 1", len(backend.copied))
	}
	if backend.copied[0] != want {
		t.Errorf("copied %q, want %q", backend.copied[0], want)
	}
}

func TestDraggingDownCopiesEveryRowItCrosses(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	want := strings.Join([]string{
		swept(t, m, 5, 30, m.width),
		swept(t, m, 6, 0, m.width),
		swept(t, m, 7, 0, 46),
	}, "\n")
	dragOver(t, m, cell{row: 5, col: 30}, cell{row: 7, col: 45})

	if len(backend.copied) != 1 {
		t.Fatalf("Copy called %d times, want 1", len(backend.copied))
	}
	if backend.copied[0] != want {
		t.Errorf("copied\n%q\nwant\n%q", backend.copied[0], want)
	}
	if !strings.Contains(m.status, "copied 3 lines") {
		t.Errorf("status = %q, want it to say three lines were copied", m.status)
	}
}

func TestAClickOnItsOwnCopiesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	dragOver(t, m, cell{row: 4, col: 10}, cell{row: 4, col: 10})

	if len(backend.copied) != 0 {
		t.Fatalf("a click copied %q", backend.copied)
	}
	if m.drag != nil {
		t.Error("a click left something marked on screen")
	}
	if strings.Contains(m.status, "copied") {
		t.Errorf("status = %q, want nothing about copying", m.status)
	}
}

func TestSweepingOnlyBlankScreenSaysSoAndCopiesNothing(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	dragOver(t, m, cell{row: 20, col: 40}, cell{row: 20, col: 60})

	if len(backend.copied) != 0 {
		t.Fatalf("blank screen copied %q", backend.copied)
	}
	if !strings.Contains(m.status, "nothing there to copy") {
		t.Errorf("status = %q, want it to say there was nothing to copy", m.status)
	}
}

func TestTextIsSweptOffTheHelpScreenToo(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)
	press(t, m, "?")

	want := swept(t, m, 1, 1, 5)
	dragOver(t, m, cell{row: 1, col: 1}, cell{row: 1, col: 4})

	if len(backend.copied) != 1 {
		t.Fatalf("Copy called %d times, want 1", len(backend.copied))
	}
	if backend.copied[0] != want {
		t.Errorf("copied %q, want %q", backend.copied[0], want)
	}
}

func TestAKeyOrTheWheelLetsTheHighlightGo(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.Msg
	}{
		{"a key", keyMsg("j")},
		{"the wheel", wheelMsg(tea.MouseButtonWheelDown, 50)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))
			dragOver(t, m, cell{row: 4, col: 10}, cell{row: 4, col: 40})
			if m.drag == nil {
				t.Fatal("the sweep left nothing marked on screen")
			}

			send(t, m, tc.msg)
			if m.drag != nil {
				t.Errorf("%s left the sweep marked on screen", tc.name)
			}
		})
	}
}

func TestResizingLetsTheHighlightGo(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))
	dragOver(t, m, cell{row: 4, col: 10}, cell{row: 4, col: 40})

	send(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.drag != nil {
		t.Error("a resize left the sweep marked on screen")
	}
}

func TestTheHighlightOnlyRestylesWhatIsAlreadyDrawn(t *testing.T) {
	m := syntaxModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	dragOver(t, m, cell{row: 2, col: 7}, cell{row: 6, col: 44})

	marked := m.View()
	m.drag = nil
	plain := ansi.Strip(m.View())
	if got := ansi.Strip(marked); got != plain {
		t.Errorf("the sweep changed what the screen says:\n%s\nwant\n%s", got, plain)
	}
	for i, line := range strings.Split(marked, "\n") {
		if got := ansi.StringWidth(line); got != m.width {
			t.Errorf("row %d is %d columns wide, want %d", i, got, m.width)
		}
	}
}

func TestTheHighlightStartsAtTheCellTheSweepDid(t *testing.T) {
	m := newModel(t, newFakeBackend(newSession(t, twoFileDiff)))

	dragOver(t, m, cell{row: 5, col: 32}, cell{row: 5, col: 41})

	want := swept(t, m, 5, 32, 42)
	if want == "" {
		t.Fatal("nothing was swept over")
	}
	head, marked, found := strings.Cut(strings.Split(m.View(), "\n")[5], ansi.ResetStyle)
	if !found {
		t.Fatal("row 5 is not marked at all")
	}
	if got := ansi.StringWidth(head); got != 32 {
		t.Errorf("the mark starts at column %d, want 32", got)
	}
	if !strings.HasPrefix(ansi.Strip(marked), want) {
		t.Errorf("the mark reads %q, want it to start %q", ansi.Strip(marked), want)
	}
}

func TestMarkingASpanKeepsTheColoursAroundIt(t *testing.T) {
	line := "\x1b[31mred\x1b[0m plain \x1b[32mgreen\x1b[0m"

	out := paintSpan(line, 4, 9, lipgloss.NewStyle().Reverse(true))

	if got := ansi.Strip(out); got != ansi.Strip(line) {
		t.Errorf("marking changed the text: %q, want %q", got, ansi.Strip(line))
	}
	if got := ansi.StringWidth(out); got != ansi.StringWidth(line) {
		t.Errorf("marked line is %d columns wide, want %d", got, ansi.StringWidth(line))
	}
	if !strings.Contains(out, "\x1b[32m") {
		t.Errorf("the colour after the mark was lost: %q", out)
	}
}
