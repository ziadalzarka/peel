package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

const (
	headerHeight = 1
	footerHeight = 2
	// filePaneMin is the narrowest useful file pane; below it the pane is
	// dropped entirely rather than shown truncated to uselessness.
	filePaneMin = 14
	filePaneMax = 30
)

// View satisfies tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	switch m.mode {
	case modeHelp:
		return m.frame(m.helpView())
	case modeWalkthrough:
		return m.frame(m.walkView())
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
	if m.width < 72 || len(m.doc.Files) == 0 {
		return 0
	}
	w := min(m.width/4, filePaneMax)
	if w < filePaneMin {
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
	case modeLineSelect:
		return "j/k line · space select · a all · n none · s stage · u unstage · c comment · esc done"
	case modeComment:
		return "ctrl+s save · esc cancel"
	case modeWalkthrough:
		return "j/k scroll · r regenerate · esc close"
	case modeHelp:
		return "any key to close"
	default:
		return `j/k hunk · J/K file · s stage · u unstage · v lines · c comment · \ layout · w walkthrough · ? help · q quit`
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
		focus := -1
		if m.mode == modeLineSelect {
			if index, ok := m.focusedLine(); ok {
				focus = index
			}
		}
		for i := m.top; i < m.top+height && i < m.doc.Len(); i++ {
			out = append(out, m.renderer.Row(m.doc, i, m.rowState(i, focus)))
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

// rowState decides how a row is marked, which is the only place the cursor and
// the line selection meet the renderer.
func (m *Model) rowState(i, focus int) RowState {
	row := m.doc.Rows[i]
	selecting := m.mode == modeLineSelect
	st := RowState{Cursor: i == m.cursor && !selecting, Selecting: selecting}

	if !selecting || row.Kind != RowLine || row.Hunk != m.lineHunk || row.Left < 0 {
		return st
	}
	lines := m.doc.Hunks[m.lineHunk].Hunk.Lines
	if row.Left >= len(lines) {
		return st
	}
	st.Selectable = lines[row.Left].IsChange()
	st.Selected = m.selected[row.Left]
	st.Focus = row.Left == focus
	return st
}

// fileLines draws the file overview, or nil when the pane does not fit.
func (m *Model) fileLines(height int) []string {
	width := m.filePaneWidth()
	if width == 0 {
		return nil
	}

	current := m.doc.FileAt(m.cursor)
	start := 0
	if current >= height {
		start = current - height + 1
	}

	out := make([]string, 0, height)
	for i := start; i < start+height; i++ {
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
	name := shorten(entry.Path, max(room, 4))

	marker := " "
	styled := name
	if current {
		marker = m.theme.Cursor.Render("▌")
		styled = m.theme.Cursor.Render(name)
	}
	line := marker + symbol + " " + styled
	return fit(line, width-ansi.StringWidth(counts)-1) + m.theme.Dim.Render(counts) + " "
}

func (m *Model) commentView() string {
	width := m.width
	lines := []string{
		fit(" "+m.theme.Header.Render("Comment on ")+m.theme.FileHead.Render(m.pending.location()), width),
		fit("", width),
	}
	for _, l := range strings.Split(m.input.View(), "\n") {
		lines = append(lines, fit(" "+l, width))
	}
	return strings.Join(padLines(lines, m.bodyHeight(), width), "\n")
}

func (m *Model) walkView() string {
	if !m.walkLoaded {
		note := "generating a walkthrough…"
		if m.err != nil {
			note = "no walkthrough available"
		}
		return strings.Join(padLines([]string{fit(" "+m.theme.Note.Render(note), m.width)}, m.bodyHeight(), m.width), "\n")
	}
	lines := strings.Split(m.walk.View(), "\n")
	for i, l := range lines {
		lines[i] = fit(l, m.width)
	}
	return strings.Join(padLines(lines, m.bodyHeight(), m.width), "\n")
}

// helpBindings is the single source of truth for the help screen. The footer
// hints are deliberately shorter; this is the full list.
var helpBindings = []struct{ keys, action string }{
	{"j / k", "next / previous hunk"},
	{"J / K", "next / previous file"},
	{"g / G", "first / last hunk"},
	{"ctrl+d / ctrl+u", "half a page down / up"},
	{"tab", "collapse or expand the file"},
	{"s", "stage the hunk, or the file on a file header"},
	{"u", "unstage the hunk or the file"},
	{"a / U", "stage everything / unstage everything"},
	{"v", "select individual lines to stage"},
	{"c", "comment at the cursor"},
	{"x", "resolve or reopen the comment at the cursor"},
	{"D", "delete the comment at the cursor"},
	{`\`, "toggle unified and side-by-side"},
	{"w", "walkthrough (r regenerates)"},
	{"r", "reload from git"},
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
