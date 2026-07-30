package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// lineNumWidth is the width of one line-number column in the gutter.
const lineNumWidth = 4

// tabWidth is how far a tab advances the cursor.
const tabWidth = 8

const (
	resetSequence = "\x1b[0m"
	fillProbe     = "x"
)

// splitDivider separates the two sides of a split row.
const splitDivider = " │ "

// RowState is what a row needs from outside the document to be drawn.
type RowState struct {
	// Cursor marks the row the cursor rests on.
	Cursor bool
	// Draft is the line of the comment editor a draft row shows. The editor
	// arrives per frame rather than through the document, since it changes on
	// every keystroke and the document does not.
	Draft string
}

// Renderer turns document rows into terminal lines.
type Renderer struct {
	theme  Theme
	syntax *Highlighter
	width  int
	// xoff is how many columns of code have been slid off the left edge, for
	// reading a line too long for the pane.
	xoff int

	addedFill   string
	removedFill string
}

// NewRenderer returns a renderer. A nil syntax highlighter disables colouring
// by language.
func NewRenderer(theme Theme, syntax *Highlighter) *Renderer {
	return &Renderer{
		theme:       theme,
		syntax:      syntax,
		width:       80,
		addedFill:   fillSequence(theme.AddedFill),
		removedFill: fillSequence(theme.RemovedFill),
	}
}

// SetWidth sets the width rows are fitted to.
func (r *Renderer) SetWidth(w int) {
	r.width = max(w, 20)
}

// SetOffset slides the code sideways by off columns. Only the code moves: the
// line numbers and the +/- origin are pinned, since they are what says which
// line is being read and a row whose identity has scrolled away is worse than
// one whose tail has.
func (r *Renderer) SetOffset(off int) {
	r.xoff = max(off, 0)
}

// CodeColumns is how much of a line of code fits on screen, after the gutter and
// the origin character have taken their columns. It is what an offset is
// measured against, so scrolling right cannot run past the longest line.
func (r *Renderer) CodeColumns(l Layout) int {
	body := r.width - 1
	if l == LayoutSplit {
		half := (body - ansi.StringWidth(splitDivider)) / 2
		return max(half-lineNumWidth-2, 1)
	}
	return max(body-2*lineNumWidth-2, 1)
}

// Row renders one row of the document to a single line.
func (r *Renderer) Row(d Document, i int, st RowState) string {
	if i < 0 || i >= len(d.Rows) {
		return ""
	}
	row := d.Rows[i]
	switch row.Kind {
	case RowFile:
		return r.file(d, row, st)
	case RowHunk:
		return r.hunk(d, row, st)
	case RowLine:
		return r.line(d, row, st)
	case RowNote:
		return r.note(row, st)
	case RowComment:
		return r.comment(d, row, st)
	case RowDraft:
		return r.draft(st)
	case RowStep:
		return r.step(d, row, st)
	case RowStepText:
		return r.stepText(row)
	default:
		// A separator still has to fill its width, or the pane beside it shows
		// through.
		return r.fit("")
	}
}

func (r *Renderer) file(d Document, row Row, st RowState) string {
	f := d.Files[row.File]
	entry := f.Entry
	added, removed := entry.Stats()

	arrow := "▾"
	if f.Collapsed {
		arrow = "▸"
	}
	name := entry.Path
	if st.Cursor {
		name = r.theme.Cursor.Render(name)
	} else {
		name = r.theme.FileHead.Render(name)
	}

	summary := fmt.Sprintf("%s +%d -%d", fileLabel(entry), added, removed)
	return r.fit(strings.Join([]string{
		" ",
		r.stateSymbol(entry.State()),
		r.theme.Dim.Render(arrow),
		name,
		r.theme.Dim.Render(summary),
	}, " "))
}

func (r *Renderer) hunk(d Document, row Row, st RowState) string {
	ref := d.Hunks[row.Hunk]
	style := r.theme.HunkHead
	if st.Cursor {
		style = r.theme.Cursor
	}
	origin := r.theme.Dim.Render("worktree")
	if ref.Staged {
		origin = r.theme.Staged.Render("index")
	}
	title := ref.Hunk.Section
	if title == "" {
		title = "⋯"
	}
	return r.fit(r.marker(st) + codeIndent(d.Layout) + style.Render(title) + "  " + origin)
}

func codeIndent(l Layout) string {
	if l == LayoutSplit {
		return strings.Repeat(" ", lineNumWidth+2)
	}
	return strings.Repeat(" ", 2*lineNumWidth+2)
}

func (r *Renderer) line(d Document, row Row, st RowState) string {
	ref := d.Hunks[row.Hunk]
	prefix := r.marker(st)
	width := r.width - ansi.StringWidth(prefix)
	if d.Layout == LayoutSplit {
		return prefix + r.splitBody(ref, row, width)
	}
	return prefix + r.unifiedBody(ref, row, width)
}

func (r *Renderer) unifiedBody(ref HunkRef, row Row, width int) string {
	l := ref.Hunk.Lines[row.Left]
	body := r.gutter(l.OldLine) + r.gutter(l.NewLine) + " " + r.content(ref.Path, l)
	return fill(r.fillFor(l), fit(body, width))
}

// splitBody puts the old side left of the new side. Either index may be -1,
// where the change has no counterpart on that side.
func (r *Renderer) splitBody(ref HunkRef, row Row, width int) string {
	divider := r.theme.Dim.Render(splitDivider)
	sides := width - ansi.StringWidth(divider)
	half := sides / 2
	left := r.halfLine(ref, row.Left, true, half)
	right := r.halfLine(ref, row.Right, false, sides-half)
	return left + divider + right
}

func (r *Renderer) halfLine(ref HunkRef, index int, old bool, width int) string {
	if index < 0 || index >= len(ref.Hunk.Lines) {
		return pad("", width)
	}
	l := ref.Hunk.Lines[index]
	num := l.NewLine
	if old {
		num = l.OldLine
	}
	return fill(r.fillFor(l), fit(r.gutter(num)+" "+r.content(ref.Path, l), width))
}

func (r *Renderer) fillFor(l git.Line) string {
	switch l.Kind {
	case git.LineAdded:
		return r.addedFill
	case git.LineRemoved:
		return r.removedFill
	default:
		return ""
	}
}

// content renders a line's origin character and text. The origin keeps the diff
// colour while the text keeps the language's, so syntax highlighting and the
// add/remove signal do not fight over the same characters.
//
// The horizontal offset slides the text and leaves the origin behind, so a diff
// scrolled sideways still reads as a diff.
func (r *Renderer) content(path string, l git.Line) string {
	origin := string(l.Kind.Origin())
	text := expandTabs(l.Text)
	if l.Kind == git.LineNoNewline {
		return r.theme.Dim.Render(origin + shift(text, r.xoff))
	}
	if r.syntax.Active() {
		return r.styleFor(l).Render(origin) + shift(r.syntax.Line(path, text), r.xoff)
	}
	return r.styleFor(l).Render(origin + shift(text, r.xoff))
}

// shift drops the first off columns of a line.
//
// It runs after highlighting rather than before, so chroma still lexes the whole
// line and a cut landing inside a token keeps the token's colour: escapes opened
// to the left of the cut are carried through even though the text they styled is
// gone.
func shift(s string, off int) string {
	if off <= 0 {
		return s
	}
	return ansi.TruncateLeft(s, off, "")
}

// expandTabs replaces tabs with spaces up to the next tab stop.
//
// It has to happen before anything measures the line: a tab is zero columns to
// ansi.StringWidth but eight on screen, so a tab-indented line left as it is
// gets padded to the pane width, overflows once the terminal expands it, and
// wraps — which makes the frame a line taller than the terminal and scrolls the
// whole display on every repaint.
//
// Stops are counted from the start of the text rather than the start of the
// row, so the code on a `+` line and the code on a `-` line indent alike.
func expandTabs(s string) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for _, c := range s {
		switch c {
		case '\t':
			n := tabWidth - col%tabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case '\n':
			b.WriteRune(c)
			col = 0
		default:
			b.WriteRune(c)
			col += ansi.StringWidth(string(c))
		}
	}
	return b.String()
}

func (r *Renderer) note(row Row, st RowState) string {
	return r.fit(r.marker(st) + "  " + r.theme.Note.Render(expandTabs(row.Text)))
}

func (r *Renderer) comment(d Document, row Row, st RowState) string {
	c := d.Comments[row.Comment]
	text := expandTabs(row.Text)
	body := r.theme.Comment.Render(text)
	if c.Resolved {
		body = r.theme.Resolved.Render(text)
	}

	gap := strings.Repeat(" ", commentIndent-1)
	bar := r.theme.Comment.Render("┃")
	if !row.Head {
		indent := strings.Repeat(" ", ansi.StringWidth(commentTag(c)))
		return r.fit(r.marker(st) + gap + bar + " " + indent + body)
	}
	tag := r.theme.Author.Render(commentTag(c))
	if st.Cursor {
		tag = r.theme.Cursor.Render(commentTag(c))
	}
	return r.fit(r.marker(st) + gap + bar + " " + tag + body)
}

// commentIndent is how far a comment's bar sits from the left edge, marker
// included. The editor for a comment being written is indented to match, so it
// stands exactly where the comment will.
const commentIndent = 4

// draft renders one line of the comment editor. The editor draws its own bar —
// the same ┃ a saved comment carries — so the only thing left to do here is put
// it in the comment column and fit it to the pane.
func (r *Renderer) draft(st RowState) string {
	return r.fit(r.marker(st) + strings.Repeat(" ", commentIndent-1) + st.Draft)
}

// commentTag prefixes a comment with who wrote it, and marks it resolved.
func commentTag(c store.Comment) string {
	tag := string(c.Author)
	if tag == "" {
		tag = "user"
	}
	if c.Resolved {
		tag = "✓ " + tag
	}
	return tag + ": "
}

func (r *Renderer) marker(st RowState) string {
	if st.Cursor {
		return r.theme.Cursor.Render("▌")
	}
	return " "
}

func (r *Renderer) stateSymbol(s git.StageState) string {
	switch s {
	case git.StateStaged:
		return r.theme.Staged.Render(s.Symbol())
	case git.StatePartial:
		return r.theme.Partial.Render(s.Symbol())
	default:
		return s.Symbol()
	}
}

func (r *Renderer) gutter(n int) string {
	if n <= 0 {
		return r.theme.Gutter.Render(strings.Repeat(" ", lineNumWidth))
	}
	return r.theme.Gutter.Render(pad(strconv.Itoa(n), lineNumWidth))
}

func (r *Renderer) styleFor(l git.Line) lipgloss.Style {
	switch l.Kind {
	case git.LineAdded:
		return r.theme.Added
	case git.LineRemoved:
		return r.theme.Removed
	case git.LineNoNewline:
		return r.theme.Dim
	default:
		return r.theme.Context
	}
}

func (r *Renderer) fit(s string) string { return fit(s, r.width) }

// fileLabel describes how a file changed, for the header summary.
func fileLabel(e git.FileEntry) string {
	if e.Untracked {
		return "untracked"
	}
	diff := e.Primary()
	if diff == nil {
		return ""
	}
	if diff.IsBinary {
		return "binary"
	}
	if diff.Status == git.StatusRenamed {
		return "renamed"
	}
	return string(diff.Status)
}

// fill paints seq behind an already-fitted row.
//
// Every escape inside the row — one per syntax token — ends in a reset, which
// clears the background along with the colour, so the sequence has to be armed
// again after each of them or the tint stops at the first coloured word.
func fill(seq, s string) string {
	if seq == "" {
		return s
	}
	for _, reset := range []string{resetSequence, ansi.ResetStyle} {
		s = strings.ReplaceAll(s, reset, reset+seq)
	}
	return seq + s + resetSequence
}

// fillSequence is the escape a background-only style opens with, or "" when the
// terminal takes no colour and the style renders its text bare.
func fillSequence(style lipgloss.Style) string {
	rendered := style.Render(fillProbe)
	if i := strings.Index(rendered, fillProbe); i > 0 {
		return rendered[:i]
	}
	return ""
}

// fit truncates to width and pads short lines, so callers can compose columns
// without counting ANSI escapes themselves.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > width {
		return ansi.Truncate(s, width, "…")
	}
	return s + strings.Repeat(" ", width-ansi.StringWidth(s))
}

// pad right-aligns s, for line numbers.
func pad(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// padRight left-aligns s, for label columns.
func padRight(s string, width int) string {
	if ansi.StringWidth(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-ansi.StringWidth(s))
}
