package tui

import (
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type cell struct{ row, col int }

func (c cell) before(other cell) bool {
	if c.row != other.row {
		return c.row < other.row
	}
	return c.col < other.col
}

type drag struct {
	from cell
	to   cell
	held bool
}

func (d *drag) span() (start, end cell) {
	if d.to.before(d.from) {
		return d.to, d.from
	}
	return d.from, d.to
}

func (d *drag) empty() bool { return d.from == d.to }

func (m *Model) dragMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	at := cell{row: msg.Y, col: msg.X}
	switch {
	case msg.Button == tea.MouseButtonLeft && msg.Action == tea.MouseActionPress:
		m.drag = &drag{from: at, to: at, held: true}
		return nil, true
	case m.drag == nil || !m.drag.held:
		return nil, false
	case msg.Action == tea.MouseActionMotion:
		m.drag.to = at
		return nil, true
	case msg.Action == tea.MouseActionRelease:
		m.drag.to, m.drag.held = at, false
		return m.copyDragged(), true
	}
	return nil, false
}

func (m *Model) copyDragged() tea.Cmd {
	if m.drag == nil || m.drag.empty() {
		m.drag = nil
		return nil
	}
	text := m.draggedText()
	if strings.TrimSpace(text) == "" {
		m.drag = nil
		m.status = "nothing there to copy"
		return nil
	}

	before := m.snapshot()
	m.status = "copied " + plural(strings.Count(text, "\n")+1, "line")
	backend, ctx := m.backend, m.ctx
	return func() tea.Msg {
		if err := backend.Copy(ctx, text); err != nil {
			return revertMsg{err: err, before: before}
		}
		return nil
	}
}

func (m *Model) draggedText() string {
	if m.drag == nil || m.drag.empty() {
		return ""
	}
	start, end := m.drag.span()
	var lines []string
	for row := max(start.row, 0); row <= end.row && row < len(m.screen); row++ {
		lo, hi := m.dragColumns(row, start, end)
		lines = append(lines, strings.TrimRight(ansi.Strip(ansi.Cut(m.screen[row], lo, hi)), " "))
	}
	return strings.Join(lines, "\n")
}

func (m *Model) dragColumns(row int, start, end cell) (lo, hi int) {
	lo, hi = 0, m.width
	if row == start.row {
		lo = start.col
	}
	if row == end.row {
		hi = end.col + 1
	}
	return max(lo, 0), min(hi, m.width)
}

func (m *Model) paintDrag(lines []string) []string {
	if m.drag == nil || m.drag.empty() {
		return lines
	}
	start, end := m.drag.span()
	painted := slices.Clone(lines)
	for row := max(start.row, 0); row <= end.row && row < len(painted); row++ {
		lo, hi := m.dragColumns(row, start, end)
		painted[row] = paintSpan(painted[row], lo, hi, m.theme.Selected)
	}
	return painted
}

func paintSpan(line string, lo, hi int, style lipgloss.Style) string {
	swept := ansi.Strip(ansi.Cut(line, lo, hi))
	if hi <= lo || swept == "" {
		return line
	}
	return ansi.Truncate(line, lo, "") + ansi.ResetStyle + style.Render(swept) + ansi.TruncateLeft(line, hi, "")
}
