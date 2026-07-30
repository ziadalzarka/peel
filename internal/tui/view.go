package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	headerHeight = 1
	footerHeight = 2
	// filePaneMin is the narrowest useful file pane, and the width the pane
	// holds on to as the terminal narrows — the file list stays on screen
	// rather than coming and going with the window.
	filePaneMin = 14
	filePaneMax = 30
	// diffPaneMin is the narrowest diff worth keeping the pane beside. Below
	// it the pane is dropped, since a diff squeezed past this is unreadable.
	diffPaneMin = 30
	// filePaneNameMin is the room a path needs before the pane is worth
	// spending width on the +/- counts as well.
	filePaneNameMin = 10
)

// View satisfies tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	switch m.mode {
	case modeHelp:
		return m.frame(m.helpView())
	case modeComment:
		return m.frame(m.commentView())
	default:
		return m.frame(m.bodyView())
	}
}

// frame stacks the header, a body of exactly bodyHeight lines, and the footer.
func (m *Model) frame(body string) string {
	return m.headerView() + "\n" + body + "\n" + m.footerView()
}

func (m *Model) bodyHeight() int {
	return max(m.height-headerHeight-footerHeight, 1)
}

func (m *Model) filePaneWidth() int {
	if len(m.doc.Files) == 0 {
		return 0
	}
	w := min(max(m.width/4, filePaneMin), filePaneMax)
	if m.width-w-1 < diffPaneMin {
		return 0
	}
	return w
}

func (m *Model) diffWidth() int {
	pane := m.filePaneWidth()
	if pane == 0 {
		return m.width
	}
	return max(m.width-pane-1, 20)
}

func (m *Model) headerView() string {
	added, removed := m.session.Stats()
	left := strings.Join([]string{
		m.theme.Title.Render("peel"),
		m.theme.Header.Render(m.session.Title),
		m.theme.Dim.Render(fmt.Sprintf("%s  +%d -%d", plural(len(m.session.Files), "file"), added, removed)),
	}, "  ")

	right := []string{m.theme.Dim.Render(m.layout.String())}
	if len(m.doc.Steps) > 0 {
		walk := []string{m.theme.Title.Render("walkthrough"), m.theme.Dim.Render(m.doc.StepSummary())}
		if m.walkStale {
			walk = append(walk, m.theme.Partial.Render("stale"))
		}
		right = append(walk, right...)
	}
	if m.follow {
		right = append([]string{m.theme.Status.Render("following")}, right...)
	}
	if n := len(m.comments); n > 0 {
		right = append([]string{m.theme.Comment.Render(plural(n, "comment"))}, right...)
	}
	if !m.session.Stageable {
		right = append([]string{m.theme.Partial.Render("read-only")}, right...)
	}
	if m.busy != "" {
		right = append([]string{m.theme.Status.Render(m.busy + "…")}, right...)
	}
	return spread(left, strings.Join(right, "  "), m.width)
}

func (m *Model) footerView() string {
	var status string
	switch {
	case m.err != nil:
		status = m.theme.Error.Render(m.err.Error())
	case m.status != "":
		status = m.theme.Status.Render(m.status)
	}
	return fit(status, m.width) + "\n" + fit(m.theme.Footer.Render(m.hints()), m.width)
}

// hints lists the keys that do something in the current mode.
func (m *Model) hints() string {
	switch m.mode {
	case modeComment:
		return "ctrl+s save · esc cancel"
	case modeHelp:
		return "any key to close"
	default:
		return `j/k hunk · ↓/↑ line · J/K file · s stage file · u unstage · tab fold · c comment · \ layout · w walkthrough · ? help · q quit`
	}
}

// bodyView draws the file pane beside the diff.
func (m *Model) bodyView() string {
	height := m.bodyHeight()
	diff := m.diffLines(height)

	pane := m.fileLines(height)
	if pane == nil {
		return strings.Join(diff, "\n")
	}
	rows := make([]string, height)
	for i := range rows {
		rows[i] = pane[i] + m.theme.Dim.Render("│") + diff[i]
	}
	return strings.Join(rows, "\n")
}

func (m *Model) diffLines(height int) []string {
	width := m.diffWidth()
	out := make([]string, 0, height)

	if m.doc.Len() == 0 {
		out = append(out, fit(" "+m.theme.Note.Render(m.emptyMessage()), width))
	} else {
		for i := m.top; i < m.top+height && i < m.doc.Len(); i++ {
			out = append(out, m.renderer.Row(m.doc, i, RowState{Cursor: i == m.cursor}))
		}
	}
	return padLines(out, height, width)
}

func (m *Model) emptyMessage() string {
	if m.session.Stageable {
		return "nothing to review — the working tree is clean"
	}
	return "nothing to review"
}

// fileLines draws the file overview, or nil when the pane does not fit.
//
// The pane scrolls on its own window, m.fileTop, so it can be moved without
// moving the diff and vice versa.
func (m *Model) fileLines(height int) []string {
	width := m.filePaneWidth()
	if width == 0 {
		return nil
	}

	current := m.markedFile()
	out := make([]string, 0, height)
	for i := m.fileTop; i < m.fileTop+height; i++ {
		if i >= len(m.doc.Files) {
			out = append(out, strings.Repeat(" ", width))
			continue
		}
		out = append(out, m.fileRow(i, i == current, width))
	}
	return out
}

func (m *Model) fileRow(index int, current bool, width int) string {
	entry := m.doc.Files[index].Entry
	added, removed := entry.Stats()
	counts := fmt.Sprintf("+%d -%d", added, removed)

	symbol := m.renderer.stateSymbol(entry.State())
	// 3 for the marker and the two spaces around the symbol, 1 for the gap.
	room := width - 4 - ansi.StringWidth(counts) - 1
	// Now that the pane stays on screen at narrow widths, it can end up with
	// room for the counts or for the path but not both. The path wins: the
	// file header in the diff carries the counts anyway.
	if room < filePaneNameMin {
		counts, room = "", width-4
	}
	name := shorten(entry.Path, max(room, 4))

	marker := " "
	styled := name
	if current {
		marker = m.theme.Cursor.Render("▌")
		styled = m.theme.Cursor.Render(name)
	}
	line := marker + symbol + " " + styled
	if counts == "" {
		return fit(line, width)
	}
	return fit(line, width-ansi.StringWidth(counts)-1) + m.theme.Dim.Render(counts) + " "
}

func (m *Model) commentView() string {
	width := m.width
	lines := []string{
		fit(" "+m.theme.Dim.Render("Comment on ")+m.theme.FileHead.Render(m.pending.location()), width),
		fit("", width),
	}
	for _, l := range strings.Split(m.input.View(), "\n") {
		lines = append(lines, fit(" "+l, width))
	}
	return strings.Join(padLines(lines, m.bodyHeight(), width), "\n")
}

// helpBindings is the single source of truth for the help screen. The footer
// hints are deliberately shorter; this is the full list.
var helpBindings = []struct{ keys, action string }{
	{"j / k", "next / previous hunk, file or comment"},
	{"↓ / ↑", "move the cursor one line (the wheel scrolls the diff)"},
	{"J / K", "next / previous file"},
	{"] / [", "scroll the file list on its own"},
	{"g / G", "first / last row"},
	{"ctrl+d / ctrl+u", "half a page down / up"},
	{"tab", "collapse the file, or fold a walkthrough note away"},
	{"s", "stage the file the cursor is in — it folds away once staged"},
	{"u", "unstage that file, opening it again"},
	{"a / U", "stage everything / unstage everything"},
	{"c", "comment at the cursor, changed line or not"},
	{"x", "resolve or reopen the comment at the cursor"},
	{"D", "delete the comment at the cursor"},
	{`\`, "toggle unified and side-by-side"},
	{"w", "walkthrough: group the diff into steps, with a note before each"},
	{"W", "regenerate the walkthrough"},
	{"r", "reload from git"},
	{"f", "follow: re-read the repository as it changes"},
	{"? / q", "help / quit"},
}

func (m *Model) helpView() string {
	lines := []string{fit(" "+m.theme.Header.Render("Keys"), m.width), fit("", m.width)}
	for _, b := range helpBindings {
		lines = append(lines, fit(" "+m.theme.Key.Render(padRight(b.keys, 17))+" "+b.action, m.width))
	}
	lines = append(lines,
		fit("", m.width),
		fit(" "+m.theme.Note.Render("Staging happens here, never from the command line."), m.width),
	)
	return strings.Join(padLines(lines, m.bodyHeight(), m.width), "\n")
}

// padLines trims or extends lines to exactly height rows of the given width, so
// the body never pushes the footer off screen.
func padLines(lines []string, height, width int) []string {
	if len(lines) > height {
		return lines[:height]
	}
	blank := strings.Repeat(" ", max(width, 0))
	for len(lines) < height {
		lines = append(lines, blank)
	}
	return lines
}

// spread puts left and right on one line of the given width, with the gap
// between them.
func spread(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return fit(left, width)
	}
	return left + strings.Repeat(" ", gap) + right
}

// shorten trims a path from the left, since the file name matters more than the
// directories above it.
func shorten(path string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(path) <= width {
		return path
	}
	runes := []rune(path)
	if width <= 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(width-1):])
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
