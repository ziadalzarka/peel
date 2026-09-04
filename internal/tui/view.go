package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/app"
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
	// filePaneNameMin is the room a name needs before the pane is worth
	// spending width on the +/- counts as well.
	filePaneNameMin = 10
	// filePaneGutter is what every row of the pane spends before its name: the
	// cursor marker, the state symbol, and the space after it.
	filePaneGutter = 3
)

// treeGuide is what one level of nesting costs a row of the file pane.
const treeGuide = "│ "

// View satisfies tea.Model.
func (m *Model) View() string {
	if m.quitting {
		return ""
	}
	if m.mode == modeHelp {
		return m.frame(m.helpView())
	}
	// The comment editor has no screen of its own: it is drawn into the diff at
	// the anchor it will attach to, so what is being commented on stays in front
	// of the reviewer writing about it.
	return m.frame(m.bodyView())
}

// frame stacks the header, a body of exactly bodyHeight lines, and the footer.
func (m *Model) frame(body string) string {
	return m.headerView() + "\n" + body + "\n" + m.footerView()
}

func (m *Model) bodyHeight() int {
	return max(m.height-headerHeight-footerHeight, 1)
}

// filePaneWidth is how wide the list of files beside the diff is, and 0 when
// there is no list.
//
// It is measured off the session rather than the laid-out document, because the
// document is laid out to the width this leaves: a comment is wrapped to the
// room the diff has, and asking the half-built document how wide it is would
// wrap the first one to a pane that is about to appear.
func (m *Model) filePaneWidth() int {
	if m.filePaneOff || m.session == nil || len(m.session.Files) == 0 {
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
	// Scrolled sideways, a run of short lines looks like a diff that has lost
	// its code. Saying which column the pane starts at is what explains it.
	if m.xoff > 0 {
		right = append(right, m.theme.Dim.Render(fmt.Sprintf("col %d", m.xoff+1)))
	}
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
	// Hidden notes are said to be hidden, so a diff the agent has commented on
	// does not read as one it has not.
	if m.agentCommentsOff {
		right = append([]string{m.theme.Partial.Render("agent hidden")}, right...)
	}
	if n := len(m.visibleComments()); n > 0 {
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
	case m.ask != nil:
		status = m.theme.Status.Render(m.ask.question)
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
		return "enter save · shift/alt+enter new line · esc cancel"
	case modeHelp:
		if m.helpBottom(m.helpLines()) > 0 {
			return "↓/↑ read on · any other key closes"
		}
		return "any key to close"
	case modeConfirm:
		return "y confirm · any other key cancels"
	case modeFind:
		return "type part of a path · ↓/↑ choose · enter go there · esc cancel"
	case modeReview:
		return "write a summary · enter continue · shift/alt+enter new line · esc cancel"
	case modeReviewEvent:
		return "a approve · r request changes · c comment · esc cancel"
	default:
		stage := "s stage file · S stage by hunk"
		if m.stageMode == app.StageModeHunk {
			stage = "s stage hunk · S stage by file"
		}
		hints := `j/k hunk · ↓/↑ line · [/] ten lines · opt+↓/↑ file · cmd+p go to file · shift+↓/↑ mark · ` +
			stage + ` · u unstage · space fold · c comment · b files · \ layout · w walkthrough`
		// The key that posts is only worth a place in the footer where there is
		// something to post to.
		if m.session != nil && m.session.PR != nil {
			hints += " · P post review"
		}
		return hints + " · ? help · q quit"
	}
}

// bodyView draws the file pane beside the diff, with the file search over the
// foot of both while one is open.
func (m *Model) bodyView() string {
	height := m.bodyHeight()
	rows := m.diffLines(height)

	if pane := m.fileLines(height); pane != nil {
		for i := range rows {
			rows[i] = pane[i] + m.theme.Dim.Render("│") + rows[i]
		}
	}
	if m.mode == modeFind {
		rows = overlay(rows, m.findLines(m.width))
	}
	if m.mode == modeReview || m.mode == modeReviewEvent {
		rows = overlay(rows, m.reviewLines(m.width))
	}
	return strings.Join(rows, "\n")
}

func (m *Model) diffLines(height int) []string {
	width := m.diffWidth()
	out := make([]string, 0, height)

	if m.doc.Len() == 0 {
		out = append(out, fit(" "+m.theme.Note.Render(m.emptyMessage()), width))
	} else {
		editor := m.draftLines()
		lo, hi, marked := m.selectedRows()
		for i := m.top; i < m.top+height && i < m.doc.Len(); i++ {
			st := RowState{
				Cursor: i == m.cursor,
				// Only the lines themselves: a comment already sitting inside the
				// run is not part of what a new note would be about.
				Marked: marked && i >= lo && i <= hi && m.doc.Rows[i].Kind == RowLine,
				Draft:  nthLine(editor, i-m.doc.DraftRow),
			}
			out = append(out, m.renderer.Row(m.doc, i, st))
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

// fileLines draws the file tree, or nil when the pane does not fit.
//
// The pane scrolls on its own window, m.fileTop, so it can be moved without
// moving the diff and vice versa.
func (m *Model) fileLines(height int) []string {
	width := m.filePaneWidth()
	if width == 0 {
		return nil
	}

	marked := m.markedPath()
	out := make([]string, 0, height)
	for i := m.fileTop; i < m.fileTop+height; i++ {
		if i >= len(m.fileRows) {
			out = append(out, strings.Repeat(" ", width))
			continue
		}
		out = append(out, m.paneLine(m.fileRows[i], marked, width))
	}
	return out
}

// paneLine draws one row of the tree. marked is the path of the file the pane
// is pointing at.
func (m *Model) paneLine(row paneRow, marked string, width int) string {
	guides := strings.Repeat(treeGuide, row.Depth)
	indent := m.theme.Dim.Render(guides)
	room := width - filePaneGutter - ansi.StringWidth(guides)

	if row.File < 0 {
		// A directory carries the state of the files under it, so one already
		// worked through reads as done at a glance. The directories above the
		// marked file are undimmed, which is what makes the mark a place in the
		// tree rather than a file on its own.
		style := m.theme.Dim
		if strings.HasPrefix(marked, row.Path+"/") {
			style = m.theme.FileHead
		}
		name := shorten(row.Name+"/", max(room, 2))
		return fit(" "+m.renderer.dirSymbol(row.State)+" "+indent+style.Render(name), width)
	}

	added, removed := m.doc.Files[row.File].Entry.Stats()
	counts := fmt.Sprintf("+%d -%d", added, removed)
	if m.doc.Files[row.File].Orphan {
		// A file with no diff has nothing to count, and `+0 -0` beside it would
		// read as a change that came to nothing. The row is here for the notes on
		// it, which the diff itself says.
		counts = ""
	}
	// What the name has left once the counts and the gap in front of them are
	// out of the way. Now that the pane stays on screen at narrow widths, and
	// an indent takes some of what is left, a row can end up with room for the
	// counts or for the name but not both. The name wins: the file header in
	// the diff carries the counts anyway.
	if counts != "" {
		if named := room - ansi.StringWidth(counts) - 2; named >= filePaneNameMin {
			room = named
		} else {
			counts = ""
		}
	}
	name := shorten(row.Name, max(room, 4))

	marker := " "
	styled := name
	if row.Path == marked {
		marker = m.theme.Cursor.Render("▌")
		styled = m.theme.Cursor.Render(name)
	}
	line := marker + m.renderer.stateSymbol(row.State) + " " + indent + styled
	if counts == "" {
		return fit(line, width)
	}
	return fit(line, width-ansi.StringWidth(counts)-1) + m.theme.Dim.Render(counts) + " "
}

// nthLine returns line n of the comment editor, or "" for a row that is not one
// of its.
func nthLine(lines []string, n int) string {
	if n < 0 || n >= len(lines) {
		return ""
	}
	return lines[n]
}

// binding is one row of the help screen: the keys, and what they do.
type binding struct{ keys, action string }

// helpBindings is the single source of truth for the help screen. The footer
// hints are deliberately shorter; this is the full list.
//
// What `s` does is the mode the review is in rather than one fixed line, since
// a help screen naming the size the reviewer is not staging in would be worse
// than no help at all.
func (m *Model) helpBindings() []binding {
	stage := binding{"s", "stage the file the cursor is in — it folds away and the next one opens"}
	if m.stageMode == app.StageModeHunk {
		stage = binding{"s", "stage the hunk the cursor is in — twice over takes the whole file"}
	}
	return []binding{
		{"j / k", "next / previous hunk, file or comment"},
		{"↓ / ↑", "move the cursor one line (the wheel scrolls the diff)"},
		{"] / [", "ten lines down / up, stopping short at any heading, note or ▴/▾ row"},
		{"opt+↓ / opt+↑", "next / previous file"},
		{"cmd+p", "go to a file by name, in the terminals that report the key"},
		{"} / {", "scroll the file tree on its own"},
		{"h / l", "scroll sideways, for a line or a path too long for the pane"},
		{"0 / $", "back to the first column / out to the longest row's end"},
		{"b", "hide or show the file tree, giving the diff the whole width"},
		{"g / G", "first / last row — cmd+↑ / cmd+↓ too, where the terminal sends them"},
		{"ctrl+d / ctrl+u", "half a page down / up"},
		{"space", "fold a file, a staged half or a note away — or, on a ▴/▾ row, read in more code"},
		stage,
		{"S", "switch what s stages: the whole file, or the hunk the cursor is in"},
		{"u", "unstage that file, opening it again"},
		{"a / U", "stage everything / unstage everything"},
		{"o", "open the file the cursor is in, outside peel"},
		{"shift+↓ / shift+↑", "mark a run of lines to write one note about — any other key lets it go"},
		{"c", "comment at the cursor, or on the run of lines marked"},
		{"enter / shift+enter", "in the editor: save the comment / write another line"},
		{"e", "edit a comment of your own, where it stands"},
		{"x / D", "resolve or reopen / delete the comment at the cursor"},
		{"C", "copy your own comments as text, to paste into an agent"},
		{"A", "hide or show the comments an agent left, leaving your own"},
		{"X", "delete every agent comment — it asks first"},
		{"P", "post the review to the pull request: a summary, then approve / request changes / comment"},
		{`\`, "toggle unified and side-by-side"},
		{"w", "walkthrough: group the diff into steps, with a note before each"},
		{"W", "regenerate the walkthrough"},
		{"r", "reload from git"},
		{"f", "follow: re-read the repository as it changes"},
		{"? / q", "help / quit"},
	}
}

// helpLines is the whole help screen, however tall the terminal is.
func (m *Model) helpLines() []string {
	lines := []string{fit(" "+m.theme.Header.Render("Keys"), m.width), fit("", m.width)}
	for _, b := range m.helpBindings() {
		lines = append(lines, fit(" "+m.theme.Key.Render(padRight(b.keys, 17))+" "+b.action, m.width))
	}
	return append(lines,
		fit("", m.width),
		fit(" "+m.theme.Note.Render("Staging happens here, never from the command line."), m.width),
	)
}

// helpView draws as much of that list as the terminal holds, from wherever it
// has been scrolled to.
//
// There are more keys than rows on a short terminal, and a list cut off at the
// bottom is a key that does not exist as far as anyone reading it knows — so the
// list is windowed and the footer says there is more of it.
func (m *Model) helpView() string {
	lines := m.helpLines()
	top := min(max(m.helpTop, 0), m.helpBottom(lines))
	return strings.Join(padLines(lines[top:], m.bodyHeight(), m.width), "\n")
}

// helpBottom is as far as the help screen scrolls: the last window that still
// ends on the final line, and 0 when the whole list already fits.
func (m *Model) helpBottom(lines []string) int {
	return max(len(lines)-m.bodyHeight(), 0)
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
