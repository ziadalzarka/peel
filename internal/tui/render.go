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

// splitRule is the line drawn between the two sides of a split row.
const splitRule = " │"

// splitDivider is what a split row spends between its halves: the rule, then a
// column the new side's marker stands in.
const splitDivider = splitRule + " "

// RowState is what a row needs from outside the document to be drawn.
type RowState struct {
	// Cursor marks the row the cursor rests on.
	Cursor bool
	// Marked reports that the row is one of a run of lines marked to comment on.
	Marked bool
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
		half, _ := splitHalves(body)
		return max(half-lineNumWidth-2, 1)
	}
	return max(body-lineNumWidth-2, 1)
}

// splitHalves divides what a split row has left, once the marker down its left
// edge has taken a column, between the old side and the new one — the divider
// between them coming off the top.
func splitHalves(width int) (left, right int) {
	sides := width - ansi.StringWidth(splitDivider)
	left = sides / 2
	return left, sides - left
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
	case RowSide:
		return r.side(d, row, st)
	case RowExpand:
		return r.expand(d, row, st)
	case RowNote:
		return r.note(row, st)
	case RowComment:
		return r.comment(d, row, st)
	case RowDraft:
		return r.draft(d, row, st)
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

	return r.fit(strings.Join([]string{
		" ",
		r.stateSymbol(entry.State()),
		r.theme.Dim.Render(arrow),
		name,
		r.fileSummary(entry, added, removed),
	}, " "))
}

// fileSummary counts what changed, split in two when the file is in both places
// at once.
//
// A folded part-staged file shows nothing but this line, so this is where the
// split has to be said: `+51 -0` on a file that is 47 lines staged and 4 lines
// new reads as one change of 51 lines, and the reviewer has no way to tell that
// only four of them are theirs to look at.
func (r *Renderer) fileSummary(e git.FileEntry, added, removed int) string {
	label := fileLabel(e)
	if e.State() != git.StatePartial {
		return r.theme.Dim.Render(fmt.Sprintf("%s +%d -%d", label, added, removed))
	}
	staged, stagedGone := e.Staged.Stats()
	work, workGone := e.Unstaged.Stats()
	return r.theme.Dim.Render(label) +
		"  " + r.theme.Staged.Render(fmt.Sprintf("index +%d -%d", staged, stagedGone)) +
		"  " + r.theme.Partial.Render(fmt.Sprintf("worktree +%d -%d", work, workGone))
}

// side heads one half of a part-staged file.
//
// It is drawn as a rule across the pane rather than a word at the end of a line:
// the two halves are the same green `+` lines one after the other, and what the
// reviewer needs is not a label to read but a place where one change visibly
// stops and the other starts.
func (r *Renderer) side(d Document, row Row, st RowState) string {
	ref := d.Sides[row.Side]
	style, label := r.theme.Partial, "unstaged · not in the index yet"
	arrow := "  "
	if ref.Staged {
		style, label = r.theme.Staged, "staged · already in the index"
		arrow = "▾ "
		if ref.Folded {
			arrow = "▸ "
		}
	}
	if st.Cursor {
		style = r.theme.Cursor
	}
	head := strings.Repeat(" ", sideIndent) + r.theme.Dim.Render(arrow) +
		style.Render(label) + "  " + r.theme.Dim.Render(fmt.Sprintf("+%d -%d", ref.Added, ref.Removed))
	return r.rule(r.marker(st) + head)
}

// sideIndent lines a side's arrow up under the file's own, so the two halves
// read as belonging to the header above them.
const sideIndent = 3

// rule fills the rest of the row with a horizontal line, making a heading read
// as a break in the diff rather than another line of it.
func (r *Renderer) rule(head string) string {
	used := ansi.StringWidth(head)
	if used+2 >= r.width {
		return r.fit(head)
	}
	return head + " " + r.theme.Dim.Render(strings.Repeat("─", r.width-used-1))
}

func (r *Renderer) hunk(d Document, row Row, st RowState) string {
	ref := d.Hunks[row.Hunk]
	style := r.theme.HunkHead
	if st.Cursor {
		style = r.theme.Cursor
	}
	title := ref.Hunk.Section
	if title == "" {
		title = "⋯"
	}
	head := r.marker(st) + codeIndent(d.Layout) + style.Render(title)
	// Which half this hunk is in is named here as well as on the heading above
	// it, since the heading scrolls off and a hunk halfway down a long staged
	// change has to say what it is on its own.
	if origin := r.hunkOrigin(d, ref); origin != "" {
		head += "  " + origin
	}
	return r.fit(head)
}

// hunkOrigin names the half of the file a hunk is in, and is empty for a file
// with only one — where every hunk would carry the same word and it would say
// nothing.
func (r *Renderer) hunkOrigin(d Document, ref HunkRef) string {
	if ref.File < 0 || ref.File >= len(d.Files) || d.Files[ref.File].Entry.State() != git.StatePartial {
		return ""
	}
	if ref.Staged {
		return r.theme.Staged.Render("index")
	}
	return r.theme.Partial.Render("worktree")
}

// expand draws a row standing where the diff leaves unchanged code out.
//
// It is dim and it sits in the code column, because it is not a line of the
// file: it is the place a run of them was left out, saying how many and which
// way `space` opens them.
func (r *Renderer) expand(d Document, row Row, st RowState) string {
	ref := d.Expands[row.Expand]
	style := r.theme.Note
	if st.Cursor {
		style = r.theme.Cursor
	}
	label := expandArrow(ref.Dir) + " " + plural(ref.Hidden, "line") + " hidden"
	return r.fit(r.marker(st) + codeIndent(d.Layout) + style.Render(label))
}

// expandArrow says which way a row opens: down from the hunk above it, up from
// the hunk below it, or — where one press finishes the run — both at once.
func expandArrow(d ExpandDir) string {
	switch d {
	case ExpandUp:
		return "▴"
	case ExpandAll:
		return "▴▾"
	default:
		return "▾"
	}
}

func codeIndent(l Layout) string {
	if l == LayoutSplit {
		return strings.Repeat(" ", lineNumWidth+2)
	}
	return strings.Repeat(" ", lineNumWidth+2)
}

func (r *Renderer) line(d Document, row Row, st RowState) string {
	ref := d.Hunks[row.Hunk]
	prefix := r.lineMarker(row, st)
	width := r.width - ansi.StringWidth(prefix)
	if d.Layout == LayoutSplit {
		return prefix + r.splitBody(ref, row, prefix, width)
	}
	return prefix + r.unifiedBody(ref, row, width)
}

func (r *Renderer) unifiedBody(ref HunkRef, row Row, width int) string {
	l := ref.Hunk.Lines[row.Left]
	body := r.gutter(lineNumberOf(l)) + " " + r.content(ref.Path, l)
	return fill(r.fillFor(l), fit(body, width))
}

// splitBody puts the old side left of the new side. Either index may be -1,
// where the change has no counterpart on that side. The row's marker is drawn
// once against each half.
func (r *Renderer) splitBody(ref HunkRef, row Row, marker string, width int) string {
	lw, rw := splitHalves(width)
	return r.halfLine(ref, row.Left, true, lw) + r.divider(marker) + r.halfLine(ref, row.Right, false, rw)
}

// divider is the rule down the middle of a split row, with the column after it
// the new side's marker stands in.
func (r *Renderer) divider(marker string) string {
	return r.theme.Dim.Render(splitRule) + marker
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
	tag := strings.Repeat(" ", ansi.StringWidth(commentTag(c)))
	if row.Head {
		tag = r.theme.Author.Render(commentTag(c))
		if st.Cursor {
			tag = r.theme.Cursor.Render(commentTag(c))
		}
	}
	return r.place(noteHalf(d.Layout, row.Hunk >= 0, c.Side), gap+bar+" "+tag+body, st)
}

// commentIndent is how far a comment's bar sits from the left edge, marker
// included. The editor for a comment being written is indented to match, so it
// stands exactly where the comment will.
const commentIndent = 4

// half says which side of a split row a note is drawn under.
type half int

const (
	// halfNone is a note drawn across the pane: every note in the unified
	// layout, and a note about a file rather than about a line of one.
	halfNone half = iota
	halfOld
	halfNew
)

// noteHalf places a note beside the code it is about. In the split layout that
// is the new side, where the reviewed code is — except for a note on a line the
// change removed, which is only on the old side, and hanging it off the new one
// would point at the line that replaced it or at nothing at all.
func noteHalf(l Layout, online bool, side store.Side) half {
	if l != LayoutSplit || !online {
		return halfNone
	}
	if side == store.SideOld {
		return halfOld
	}
	return halfNew
}

// room is how much of a pane this wide a note's text has, once the marker, the
// indent, the bar and — where the note hangs under one half of a split row —
// the other half and the divider have taken their columns.
func (h half) room(pane int) int {
	column := pane - 1
	if h != halfNone {
		left, right := splitHalves(column)
		column = right
		if h == halfOld {
			column = left
		}
	}
	return column - (commentIndent - 1) - 2
}

// editorRoom is the width the comment editor is given where a note hangs here:
// what the note's text will have, plus the bar and the space after it, which
// the editor draws itself.
func (h half) editorRoom(pane int) int { return h.room(pane) + 2 }

// place puts a note where the code it is about is: across the pane, or — in the
// split layout — under the half of the row that holds its line, the other half
// left as it would be beside any other row.
func (r *Renderer) place(h half, note string, st RowState) string {
	prefix := r.marker(st)
	width := r.width - ansi.StringWidth(prefix)
	if h == halfNone {
		return prefix + fit(note, width)
	}
	lw, rw := splitHalves(width)
	if h == halfOld {
		return prefix + fit(note, lw) + r.divider(prefix) + fit("", rw)
	}
	return prefix + fit("", lw) + r.divider(prefix) + fit(note, rw)
}

// draft renders one line of the comment editor. The editor draws its own bar —
// the same ┃ a saved comment carries — so the only thing left to do here is put
// it where the comment will stand.
func (r *Renderer) draft(d Document, row Row, st RowState) string {
	h := noteHalf(d.Layout, row.Hunk >= 0 && d.Draft.line > 0, d.Draft.side)
	return r.place(h, strings.Repeat(" ", commentIndent-1)+st.Draft, st)
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
	switch {
	// A note whose code was rewritten out from under it says where it was
	// written. Without the number it is a note under a file with no way to tell
	// what it was ever about.
	case c.Outdated:
		tag = fmt.Sprintf("%s (outdated · was :%s)", tag, lineSpan(c))
	// A note written on a run of lines sits under the last of them, so where it
	// starts is the one thing about it nothing else on screen says.
	case c.EndLine > c.Line:
		tag = fmt.Sprintf("%s (lines %d-%d)", tag, c.Line, c.EndLine)
	}
	return tag + ": "
}

// lineSpan is the lines a note was written on: one number, or the two ends of
// the run it covers.
func lineSpan(c store.Comment) string {
	if c.EndLine > c.Line {
		return fmt.Sprintf("%d-%d", c.Line, c.EndLine)
	}
	return strconv.Itoa(c.Line)
}

// marker is the bar down the left edge that says what a row is: the cursor, or
// a line of the run marked to comment on.
//
// The run is drawn in the comment's own colour rather than the cursor's, since
// what marks it is what it is for — and the cursor, which is always one end of
// it, keeps its own bar so the end being moved is still the brightest thing on
// screen.
func (r *Renderer) marker(st RowState) string {
	switch {
	case st.Cursor:
		return r.theme.Cursor.Render("▌")
	case st.Marked:
		return r.theme.Comment.Render("▌")
	default:
		return " "
	}
}

// lineMarker is a line's own bar. A line a note was already written about is
// barred like one being marked to write a note about now: both say the same
// thing — this line is part of a run somebody wrote about — and a run of one
// says it as much as a run of ten.
func (r *Renderer) lineMarker(row Row, st RowState) string {
	if row.Noted && !st.Cursor && !st.Marked {
		return r.theme.Comment.Render("▌")
	}
	return r.marker(st)
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

// dirSymbol is a directory's state, said quietly. A directory only reports what
// is under it, and a tree is mostly directories: drawn in the same colours as
// the files, every changed path down to a file is a bright mark, and the row
// that is actually staged or half-staged is the hardest one to pick out. So a
// directory keeps the tick that says a whole directory is done — the one thing
// a tree can say that a list cannot — dimmed to the weight of the guides beside
// it, and reports part-way as a dot small enough to read as a trace of the
// files below rather than a state of its own.
func (r *Renderer) dirSymbol(s git.StageState) string {
	switch s {
	case git.StateStaged:
		return r.theme.Dim.Render("✓")
	case git.StatePartial:
		return r.theme.Dim.Render("·")
	default:
		return " "
	}
}

func lineNumberOf(l git.Line) int {
	if l.Kind == git.LineRemoved {
		return l.OldLine
	}
	return l.NewLine
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
