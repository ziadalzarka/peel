// Package tui is peel's interactive review UI.
//
// Navigation and rendering are kept separate from bubbletea so they can be
// tested without a terminal: Document flattens a session into a list of rows,
// Renderer turns one row into a string, and Model only maps key presses onto
// those two.
package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// Layout selects how a hunk body is laid out.
type Layout int

const (
	// LayoutUnified shows one column of +/- lines.
	LayoutUnified Layout = iota
	// LayoutSplit shows old and new side by side.
	LayoutSplit
)

// Toggle returns the other layout.
func (l Layout) Toggle() Layout {
	if l == LayoutUnified {
		return LayoutSplit
	}
	return LayoutUnified
}

func (l Layout) String() string {
	if l == LayoutSplit {
		return "split"
	}
	return "unified"
}

// RowKind classifies one line of the document.
type RowKind int

const (
	// RowFile is a file header.
	RowFile RowKind = iota
	// RowHunk is a hunk header.
	RowHunk
	// RowLine is one line of a hunk body.
	RowLine
	// RowNote is an informational line, such as "binary file".
	RowNote
	// RowComment is one review comment shown at its anchor.
	RowComment
	// RowDraft is one line of the editor for a comment being written, shown at
	// the anchor the comment will attach to.
	RowDraft
	// RowStep is a walkthrough step's heading, shown above the files it covers.
	RowStep
	// RowStepText is one wrapped line of a step's explanation.
	RowStepText
	// RowSide heads one of a file's two changes, the index's or the working
	// tree's, above the hunks that belong to it.
	RowSide
	// RowBlank separates files.
	RowBlank
)

// Row is one rendered line of the document.
//
// Left and Right index into the enclosing hunk's Lines. In unified layout only
// Left is set; in split layout either side may be -1 where the other side has
// no counterpart.
//
// Every row renders to exactly one terminal line, which is what lets the
// viewport treat the document as a flat window. A comment body spanning several
// lines therefore becomes several rows, and only the first carries Head.
type Row struct {
	Kind    RowKind
	File    int
	Hunk    int
	Left    int
	Right   int
	Comment int
	// Step indexes into Document.Steps on a walkthrough row, and is -1 on every
	// other row.
	Step int
	// Side indexes into Document.Sides on a side heading, and is -1 on every
	// other row.
	Side int
	Text string
	Head bool
}

// HunkRef is one addressable hunk within the document.
type HunkRef struct {
	File int
	Path string
	// Staged reports that this hunk is in the index rather than the working
	// tree, which is what labels it on screen and what makes a reload put the
	// cursor back on whatever is still unstaged.
	Staged bool
	ID     git.HunkID
	Hunk   git.Hunk
}

// Origin names the diff this hunk was read from, which is what a note left on
// one of its lines has to record: the same line number means a different line in
// the other diff.
func (h HunkRef) Origin() store.Origin { return originOf(h.Staged) }

// originOf names the diff a staged or unstaged side was read from.
func originOf(staged bool) store.Origin {
	if staged {
		return store.OriginIndex
	}
	return store.OriginWorktree
}

// SideRef is one of a file's two changes — what the index holds, or what the
// working tree has on top of it — as the document lays it out.
type SideRef struct {
	File int
	Path string
	// Staged marks the index's side of the file, HEAD→index.
	Staged bool
	// Hunks indexes into Document.Hunks, and is empty while the side is folded.
	Hunks []int
	// Row is the position of the side's heading.
	Row int
	// Folded hides the side's hunks, leaving the heading. Only the index side
	// folds: the working tree's is what there is to review.
	Folded         bool
	Added, Removed int
}

// Origin names the diff this side is.
func (s SideRef) Origin() store.Origin { return originOf(s.Staged) }

// FileRef is one file within the document.
type FileRef struct {
	Entry git.FileEntry
	// Hunks indexes into Document.Hunks.
	Hunks []int
	// Row is the position of the file's header.
	Row int
	// Collapsed reports that the file's body is hidden.
	Collapsed bool
}

// StepRef is one walkthrough group as the document lays it out.
type StepRef struct {
	store.Step
	// Files indexes into Document.Files, in the order the step names them.
	Files []int
	// Row is the position of the step's heading.
	Row int
	// Folded hides the explanation, leaving the heading, so a walkthrough that
	// has been read can be got out of the way of the diff it describes.
	Folded bool
}

// Draft is the comment being written, laid out where the comment itself will
// appear once it is saved — so writing one neither takes the diff off screen nor
// moves the code it is about.
type Draft struct {
	anchor
	// Height is how many rows the editor needs. Zero means nothing is being
	// written, which is what leaves the document with no draft in it.
	Height int
}

// buildConfig collects the optional inputs to Build.
type buildConfig struct {
	groups Groups
	draft  Draft
	// sides overrides the default fold of a file's index side, by path. A path
	// with no entry takes the default.
	sides map[string]bool
	// commentWidth is the room a comment's text has beside its bar and tag.
	// Zero leaves a comment's lines as they were written.
	commentWidth int
}

// BuildOption customises how a document is laid out.
type BuildOption func(*buildConfig)

// WithDraft reserves rows for the comment editor at the anchor the comment will
// attach to.
func WithDraft(d Draft) BuildOption { return func(c *buildConfig) { c.draft = d } }

// WithSideFolds overrides, by path, whether a file's index side opens folded.
func WithSideFolds(folds map[string]bool) BuildOption {
	return func(c *buildConfig) { c.sides = folds }
}

// WithCommentWidth wraps a comment's text to the room it has, so a long line
// runs on to the next row instead of off the edge of the pane.
func WithCommentWidth(w int) BuildOption {
	return func(c *buildConfig) { c.commentWidth = w }
}

// Document is a session flattened into navigable rows.
type Document struct {
	Files []FileRef
	Hunks []HunkRef
	// Sides are the labelled halves of the part-staged files, in layout order.
	// A file whose changes are all in one place has none: there is nothing to
	// tell apart.
	Sides []SideRef
	Rows  []Row
	// Steps are the walkthrough groups the files are laid out under, in reading
	// order. It is empty when there is no walkthrough on screen.
	Steps    []StepRef
	Comments []store.Comment
	Layout   Layout
	// CodeWidth is the widest line of code the document holds, in screen
	// columns and with tabs already expanded. It bounds how far the diff can be
	// scrolled sideways, so scrolling right cannot empty the pane.
	CodeWidth int
	// Draft is the comment being written, if one is.
	Draft Draft
	// DraftRow is the first row of the editor, and -1 when nothing is being
	// written. The rest of the editor follows it, one row per line.
	DraftRow int
	// commentWidth is the room a comment's text is wrapped to, before its tag
	// is taken off the first row.
	commentWidth int
}

// Build flattens a session into rows. collapsed hides a file's body by path.
func Build(s *app.Session, comments []store.Comment, collapsed map[string]bool, layout Layout, opts ...BuildOption) Document {
	var cfg buildConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	doc := Document{Comments: comments, Layout: layout, Draft: cfg.draft, DraftRow: -1,
		commentWidth: cfg.commentWidth}
	if s == nil {
		return doc
	}
	idx := indexComments(comments)

	for _, group := range groupFiles(s.Files, cfg.groups.Steps) {
		doc.addStep(group, cfg.groups)
		for _, si := range group.files {
			entry := s.Files[si]
			fi := len(doc.Files)
			hidden := collapsed[entry.Path]
			doc.Files = append(doc.Files, FileRef{Entry: entry, Row: len(doc.Rows), Collapsed: hidden})
			doc.add(Row{Kind: RowFile, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1})
			doc.addComments(fi, -1, idx.takeFile(entry.Path))
			doc.addDraft(fi, -1, doc.draftOnFile(entry.Path))

			if !hidden {
				doc.addBody(fi, entry, idx, cfg.sides)
			}
			doc.addComments(fi, -1, idx.rest(entry.Path))
			// A draft whose line is not on screen — a collapsed file, or a line
			// the diff no longer holds — still has to be somewhere, and it goes
			// where the comments in the same position go: under its file.
			doc.addDraft(fi, -1, doc.Draft.path == entry.Path)
			doc.add(Row{Kind: RowBlank, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1})
		}
	}
	return doc
}

// addDraft lays the editor out here, if this is where the comment being written
// belongs and it has not already been placed.
func (d *Document) addDraft(file, hunk int, here bool) {
	if !here || d.Draft.Height <= 0 || d.DraftRow >= 0 {
		return
	}
	d.DraftRow = len(d.Rows)
	for range d.Draft.Height {
		d.add(Row{Kind: RowDraft, File: file, Hunk: hunk, Left: -1, Right: -1, Step: -1, Side: -1})
	}
}

// draftOnFile reports that the comment being written is a note on the file as a
// whole, which is where a draft with no line of its own goes.
func (d Document) draftOnFile(path string) bool {
	return d.Draft.path == path && d.Draft.line <= 0 && d.Draft.hunk == ""
}

// draftOnHunk reports that the comment being written is about a hunk that has no
// line to hang it on.
func (d Document) draftOnHunk(ref HunkRef) bool {
	return d.Draft.path == ref.Path && d.Draft.line <= 0 && d.Draft.hunk == ref.ID.String()
}

// draftOnLine reports that the comment being written anchors to either line of a
// displayed pair.
func (d Document) draftOnLine(ref HunkRef, pair linePair) bool {
	if d.Draft.path != ref.Path || d.Draft.line <= 0 || !sameOrigin(d.Draft.origin, ref) {
		return false
	}
	for _, i := range []int{pair.left, pair.right} {
		if i < 0 || i >= len(ref.Hunk.Lines) {
			continue
		}
		if anchors(ref.Hunk.Lines[i], d.Draft.side, d.Draft.line) {
			return true
		}
	}
	return false
}

// addStep puts a walkthrough group's heading and explanation in front of the
// files it covers, which is all the walkthrough is: the same diff, read in the
// order the narrative gives it, with the narrative in place.
func (d *Document) addStep(group fileGroup, groups Groups) {
	if group.step == nil {
		return
	}
	index := len(d.Steps)
	ref := StepRef{Step: *group.step, Row: len(d.Rows), Folded: groups.Folded[index]}
	// The group's files are appended by the caller, directly after this heading.
	for i := range group.files {
		ref.Files = append(ref.Files, len(d.Files)+i)
	}
	d.Steps = append(d.Steps, ref)

	// The heading carries the first file it introduces, so the file list beside
	// the diff marks the file the window is opening on rather than nothing.
	file := -1
	if len(ref.Files) > 0 {
		file = ref.Files[0]
	}
	row := Row{Kind: RowStep, File: file, Hunk: -1, Left: -1, Right: -1, Step: index, Side: -1}
	d.add(row)

	row.Kind = RowStepText
	if !ref.Folded {
		for _, line := range wrapBody(group.step.Body, groups.Width) {
			row.Text = line
			d.add(row)
		}
	}
	// A blank prose line closes the group, so the file header below it is not
	// pressed against the explanation.
	row.Text = ""
	d.add(row)
}

func (d *Document) add(r Row) { d.Rows = append(d.Rows, r) }

func (d *Document) addComments(file, hunk int, ids []int) {
	for _, ci := range ids {
		row := Row{
			Kind:    RowComment,
			File:    file,
			Hunk:    hunk,
			Left:    -1,
			Right:   -1,
			Comment: ci,
			Step:    -1,
			Side:    -1,
			Head:    true,
		}
		for _, text := range d.wrapComment(d.Comments[ci]) {
			row.Text = text
			d.add(row)
			row.Head = false
		}
	}
}

// wrapComment breaks a comment's body into the rows it takes up. The lines it
// was written with are kept — a review comment holds a list or a snippet as
// often as it holds prose — and only a line with more in it than the pane can
// hold runs on to the next row.
//
// Every row is wrapped to the same width: the tag naming the author takes room
// on the first row, and the renderer indents the rest below it to match.
func (d Document) wrapComment(c store.Comment) []string {
	lines := strings.Split(c.Body, "\n")
	if d.commentWidth <= 0 {
		return lines
	}
	width := max(d.commentWidth-ansi.StringWidth(commentTag(c)), minCommentWidth)
	var out []string
	for _, line := range lines {
		out = append(out, strings.Split(ansi.Wrap(expandTabs(line), width, " -"), "\n")...)
	}
	return out
}

// minCommentWidth keeps a comment readable in a pane too narrow to hold both
// the tag and a useful amount of text, at the cost of running past the edge.
const minCommentWidth = 20

// addBody lays out a file's changes, the index's side first.
//
// A file git holds in both places at once is drawn as two labelled halves rather
// than one run of hunks. They are separate changes measured against separate
// files — the same line number means a different line in each — and telling
// where the reviewed half ends and the new one begins is the whole difficulty of
// reading a part-staged file.
func (d *Document) addBody(fi int, entry git.FileEntry, idx *commentIndex, folds map[string]bool) {
	if entry.IsBinary() {
		d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1,
			Text: "binary file — no diff to show"})
		return
	}

	// Only a file with something in the index has two halves to tell apart. The
	// ordinary file — everything still in the working tree — is left as the plain
	// run of hunks it has always been, and a heading saying "unstaged" over every
	// file in an ordinary review says nothing.
	labelled, folded := entry.Staged != nil, false
	for _, side := range sidesOf(entry) {
		si := -1
		if labelled {
			si = d.addSide(fi, entry, side, folds)
			if d.Sides[si].Folded {
				folded = true
				continue
			}
		}
		d.addHunks(fi, entry, side, si, idx)
	}

	// A file with nothing to show says so — unless what it has is only hidden,
	// where the heading above already says where it went.
	if len(d.Files[fi].Hunks) == 0 && !folded {
		d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Text: emptyNote(entry)})
	}
}

// addSide heads one half of a part-staged file, and returns its index.
func (d *Document) addSide(fi int, entry git.FileEntry, s side, folds map[string]bool) int {
	si := len(d.Sides)
	added, removed := s.diff.Stats()
	d.Sides = append(d.Sides, SideRef{
		File:    fi,
		Path:    entry.Path,
		Staged:  s.staged,
		Row:     len(d.Rows),
		Folded:  sideFolded(entry, s, folds),
		Added:   added,
		Removed: removed,
	})
	d.add(Row{Kind: RowSide, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: si})
	return si
}

// sideFolded reports whether a side opens hidden.
//
// The index's half of a part-staged file does: it has been reviewed and put away
// already, so what is left open is what is left to read — the same rule staging
// a whole file follows. A file whose changes are all in the index keeps its diff
// on screen, since folding the only thing in it would leave a header with
// nothing under it. Either way `space` on the heading has the last word.
func sideFolded(e git.FileEntry, s side, folds map[string]bool) bool {
	if !s.staged {
		return false
	}
	if folded, set := folds[e.Path]; set {
		return folded
	}
	return e.Unstaged != nil
}

func (d *Document) addHunks(fi int, entry git.FileEntry, s side, si int, idx *commentIndex) {
	for _, h := range s.diff.Hunks {
		hi := len(d.Hunks)
		d.Hunks = append(d.Hunks, HunkRef{
			File:   fi,
			Path:   entry.Path,
			Staged: s.staged,
			ID:     s.diff.ID(h),
			Hunk:   h,
		})
		d.Files[fi].Hunks = append(d.Files[fi].Hunks, hi)
		if si >= 0 {
			d.Sides[si].Hunks = append(d.Sides[si].Hunks, hi)
		}
		d.add(Row{Kind: RowHunk, File: fi, Hunk: hi, Left: -1, Right: -1, Step: -1, Side: -1})
		d.addDraft(fi, hi, d.draftOnHunk(d.Hunks[hi]))
		d.measure(h.Lines)

		for _, pair := range pairLines(h.Lines, d.Layout) {
			d.add(Row{Kind: RowLine, File: fi, Hunk: hi, Left: pair.left, Right: pair.right, Step: -1, Side: -1})
			d.addComments(fi, hi, idx.takeLine(d.Hunks[hi], pair))
			d.addDraft(fi, hi, d.draftOnLine(d.Hunks[hi], pair))
		}
	}
}

// measure widens CodeWidth to hold a hunk's longest line. Tabs are expanded
// first, since the offset the width bounds is counted in screen columns and a
// tab is eight of them.
func (d *Document) measure(lines []git.Line) {
	for _, l := range lines {
		d.CodeWidth = max(d.CodeWidth, ansi.StringWidth(expandTabs(l.Text)))
	}
}

// emptyNote explains a file that changed without producing hunks.
func emptyNote(e git.FileEntry) string {
	diff := e.Primary()
	if diff == nil {
		return "no changes"
	}
	if diff.OldMode != "" && diff.NewMode != "" && diff.OldMode != diff.NewMode {
		return "mode " + diff.OldMode + " → " + diff.NewMode
	}
	switch diff.Status {
	case git.StatusRenamed:
		return "renamed from " + diff.OldPath
	case git.StatusCopied:
		return "copied from " + diff.OldPath
	}
	return "no textual changes"
}

type side struct {
	diff   *git.FileDiff
	staged bool
}

// sidesOf lists a file's change sides, index first so each file reads
// index-then-worktree.
func sidesOf(e git.FileEntry) []side {
	var out []side
	if e.Staged != nil {
		out = append(out, side{diff: e.Staged, staged: true})
	}
	if e.Unstaged != nil {
		out = append(out, side{diff: e.Unstaged, staged: false})
	}
	return out
}

// linePair is one displayed row of a hunk body: an old-side index, a new-side
// index, or both.
type linePair struct{ left, right int }

// pairLines lays a hunk body out for the given layout.
//
// Unified emits every line once. Split walks each run of removals and the
// additions that follow it together, so a replaced line shows its old and new
// text on the same row.
func pairLines(lines []git.Line, layout Layout) []linePair {
	if layout == LayoutUnified {
		out := make([]linePair, 0, len(lines))
		for i := range lines {
			out = append(out, linePair{left: i, right: -1})
		}
		return out
	}

	var out []linePair
	for i := 0; i < len(lines); {
		switch lines[i].Kind {
		case git.LineContext:
			out = append(out, linePair{left: i, right: i})
			i++
		case git.LineNoNewline:
			out = append(out, noNewlinePair(lines, i))
			i++
		default:
			var removed, added []int
			for ; i < len(lines) && lines[i].Kind == git.LineRemoved; i++ {
				removed = append(removed, i)
			}
			for ; i < len(lines) && lines[i].Kind == git.LineAdded; i++ {
				added = append(added, i)
			}
			for k := 0; k < max(len(removed), len(added)); k++ {
				out = append(out, linePair{left: at(removed, k), right: at(added, k)})
			}
		}
	}
	return out
}

// noNewlinePair puts the marker on whichever side the line before it belongs
// to, since that is the side missing the newline.
func noNewlinePair(lines []git.Line, i int) linePair {
	if i == 0 {
		return linePair{left: i, right: i}
	}
	switch lines[i-1].Kind {
	case git.LineAdded:
		return linePair{left: -1, right: i}
	case git.LineRemoved:
		return linePair{left: i, right: -1}
	default:
		return linePair{left: i, right: i}
	}
}

func at(s []int, i int) int {
	if i < len(s) {
		return s[i]
	}
	return -1
}

// Len is the number of rows.
func (d Document) Len() int { return len(d.Rows) }

// IsStop reports whether the cursor may rest on row i.
//
// Every row can hold the cursor except the blank between files, the
// continuation lines of a multi-line comment — a comment is addressed from its
// first line — a walkthrough explanation, which is addressed from its heading,
// and the comment editor, which has the keyboard to itself while it is open.
// Diff lines are stops, so a note can be left on unchanged code without a mode
// to enter first.
func (d Document) IsStop(i int) bool {
	if i < 0 || i >= len(d.Rows) {
		return false
	}
	switch d.Rows[i].Kind {
	case RowComment:
		return d.Rows[i].Head
	case RowBlank, RowStepText, RowDraft:
		return false
	default:
		return true
	}
}

// IsMark reports whether row i heads something whole: a file, one of its two
// changes, a hunk, a comment, or a walkthrough group. These are what j and k jump
// between, while the arrows step row by row.
func (d Document) IsMark(i int) bool {
	if i < 0 || i >= len(d.Rows) {
		return false
	}
	switch d.Rows[i].Kind {
	case RowFile, RowHunk, RowStep, RowSide:
		return true
	case RowComment:
		return d.Rows[i].Head
	default:
		return false
	}
}

// NextMark returns the next file, hunk or comment after from, or from itself.
func (d Document) NextMark(from int) int {
	for i := from + 1; i < len(d.Rows); i++ {
		if d.IsMark(i) {
			return i
		}
	}
	return from
}

// PrevMark returns the previous file, hunk or comment before from, or from
// itself.
func (d Document) PrevMark(from int) int {
	for i := from - 1; i >= 0; i-- {
		if d.IsMark(i) {
			return i
		}
	}
	return from
}

// FirstStop returns the first row the cursor may rest on, or 0.
func (d Document) FirstStop() int {
	for i := range d.Rows {
		if d.IsStop(i) {
			return i
		}
	}
	return 0
}

// NextStop returns the next cursor position after from, or from itself.
func (d Document) NextStop(from int) int {
	for i := from + 1; i < len(d.Rows); i++ {
		if d.IsStop(i) {
			return i
		}
	}
	return from
}

// PrevStop returns the previous cursor position before from, or from itself.
func (d Document) PrevStop(from int) int {
	for i := from - 1; i >= 0; i-- {
		if d.IsStop(i) {
			return i
		}
	}
	return from
}

// StopBetween returns a cursor position within the inclusive row range, or -1
// when the range holds none. fromTop picks the first one, otherwise the last —
// so a window scrolled down lands the cursor on the row nearest the edge it
// arrived from.
func (d Document) StopBetween(lo, hi int, fromTop bool) int {
	lo, hi = max(lo, 0), min(hi, len(d.Rows)-1)
	if fromTop {
		for i := lo; i <= hi; i++ {
			if d.IsStop(i) {
				return i
			}
		}
		return -1
	}
	for i := hi; i >= lo; i-- {
		if d.IsStop(i) {
			return i
		}
	}
	return -1
}

// LastStop returns the final cursor position.
func (d Document) LastStop() int {
	for i := len(d.Rows) - 1; i >= 0; i-- {
		if d.IsStop(i) {
			return i
		}
	}
	return 0
}

// NextFile returns the top of the file after the one holding from. A file whose
// top is the walkthrough heading the cursor is already on is passed over, so a
// jump forward from a heading reaches the next file rather than standing still.
func (d Document) NextFile(from int) int {
	for i := from + 1; i < len(d.Rows); i++ {
		if d.Rows[i].Kind != RowFile {
			continue
		}
		if top := d.topOf(i); top > from {
			return top
		}
	}
	return from
}

// PrevFile returns the top of the file before the one holding from, stepping to
// the top of the current file first.
func (d Document) PrevFile(from int) int {
	start := d.topOf(d.RowOfFile(d.FileAt(from)))
	if start >= 0 && start < from {
		return start
	}
	for i := from - 1; i >= 0; i-- {
		if d.Rows[i].Kind == RowFile {
			return d.topOf(i)
		}
	}
	return from
}

// topOf returns the row a jump to a file header should land on: the walkthrough
// heading that introduces the file, when there is one, so jumping forward never
// skips the note written about the group the file opens.
func (d Document) topOf(row int) int {
	for row > 0 {
		switch d.Rows[row-1].Kind {
		case RowStep, RowStepText:
			row--
		default:
			return row
		}
	}
	return row
}

// Nearest returns the closest cursor position at or after i, falling back to
// the one before it.
func (d Document) Nearest(i int) int {
	if d.IsStop(i) {
		return i
	}
	if next := d.NextStop(i); d.IsStop(next) {
		return next
	}
	return d.PrevStop(i)
}

// FileAt returns the file index the row belongs to, or -1.
func (d Document) FileAt(row int) int {
	if row < 0 || row >= len(d.Rows) {
		return -1
	}
	return d.Rows[row].File
}

// StepAt returns the walkthrough group a heading or explanation row belongs to,
// and -1 on every other row.
func (d Document) StepAt(row int) int {
	if row < 0 || row >= len(d.Rows) {
		return -1
	}
	step := d.Rows[row].Step
	if step < 0 || step >= len(d.Steps) {
		return -1
	}
	return step
}

// SideAt returns the change a side heading belongs to, and -1 on every other
// row.
func (d Document) SideAt(row int) int {
	if row < 0 || row >= len(d.Rows) {
		return -1
	}
	side := d.Rows[row].Side
	if side < 0 || side >= len(d.Sides) {
		return -1
	}
	return side
}

// RowOfSide returns the heading row of one file's index or working-tree change,
// or -1.
func (d Document) RowOfSide(path string, origin store.Origin) int {
	for _, s := range d.Sides {
		if s.Path == path && s.Origin() == origin {
			return s.Row
		}
	}
	return -1
}

// RowOfFile returns the header row of a file, or -1.
func (d Document) RowOfFile(file int) int {
	if file < 0 || file >= len(d.Files) {
		return -1
	}
	return d.Files[file].Row
}

// RowOfHunk returns the header row of a hunk, or -1.
func (d Document) RowOfHunk(hunk int) int {
	for i, r := range d.Rows {
		if r.Kind == RowHunk && r.Hunk == hunk {
			return i
		}
	}
	return -1
}

// RowOfLine returns the row displaying a hunk line, or -1.
func (d Document) RowOfLine(hunk, line int) int {
	if hunk < 0 || line < 0 {
		return -1
	}
	for i, r := range d.Rows {
		if r.Kind == RowLine && r.Hunk == hunk && (r.Left == line || r.Right == line) {
			return i
		}
	}
	return -1
}

// RowOfComment returns the first row of a comment, by ID, or -1.
func (d Document) RowOfComment(id string) int {
	if id == "" {
		return -1
	}
	for i, r := range d.Rows {
		if r.Kind == RowComment && r.Head && d.Comments[r.Comment].ID == id {
			return i
		}
	}
	return -1
}

// TargetKind says what the cursor addresses.
type TargetKind int

const (
	// TargetNone has nothing to act on.
	TargetNone TargetKind = iota
	// TargetFile is a file as a whole.
	TargetFile
	// TargetHunk is one hunk.
	TargetHunk
	// TargetLine is the lines one row of a hunk body displays.
	TargetLine
)

// Target is what the cursor addresses.
type Target struct {
	Kind TargetKind
	Path string
	File int
	Hunk int
}

// TargetAt resolves the row under the cursor to the smallest thing it names,
// which is what a comment anchors to.
//
// A comment row acts on its file, so operating on a commented line does what it
// looks like it should. A walkthrough row acts on nothing: it names a group of
// files, and commenting on a whole group from one keypress is not what the row
// looks like it means.
func (d Document) TargetAt(row int) Target {
	if row < 0 || row >= len(d.Rows) {
		return Target{}
	}
	r := d.Rows[row]
	if r.Kind == RowStep || r.Kind == RowStepText {
		return Target{}
	}
	if r.Kind == RowHunk && r.Hunk >= 0 && r.Hunk < len(d.Hunks) {
		h := d.Hunks[r.Hunk]
		return Target{Kind: TargetHunk, Path: h.Path, File: h.File, Hunk: r.Hunk}
	}
	if r.Kind == RowLine && r.Hunk >= 0 && r.Hunk < len(d.Hunks) {
		h := d.Hunks[r.Hunk]
		return Target{Kind: TargetLine, Path: h.Path, File: h.File, Hunk: r.Hunk}
	}
	if r.File < 0 || r.File >= len(d.Files) {
		return Target{}
	}
	return Target{Kind: TargetFile, Path: d.Files[r.File].Entry.Path, File: r.File, Hunk: -1}
}

// FileTargetAt resolves the row under the cursor to the file staging acts on.
//
// Staging is whole-file, so a hunk header or a diff line resolves to the file it
// belongs to rather than to itself. A walkthrough row resolves to nothing: it
// names a group of files, and staging a whole group from one keypress is not
// what the row looks like it means.
func (d Document) FileTargetAt(row int) (FileRef, bool) {
	if row < 0 || row >= len(d.Rows) {
		return FileRef{}, false
	}
	r := d.Rows[row]
	if r.Kind == RowStep || r.Kind == RowStepText {
		return FileRef{}, false
	}
	if r.File < 0 || r.File >= len(d.Files) {
		return FileRef{}, false
	}
	return d.Files[r.File], true
}

// LineAt returns the hunk and the index of the line a row addresses, for
// anchoring a comment.
//
// Where a split row shows a removal beside the addition that replaced it, the
// new side wins: a review note is usually about the code that is arriving, not
// the code leaving.
func (d Document) LineAt(row int) (HunkRef, int, bool) {
	ref, r, ok := d.hunkRow(row)
	if !ok {
		return HunkRef{}, 0, false
	}
	for _, i := range []int{r.Right, r.Left} {
		if i >= 0 && i < len(ref.Hunk.Lines) {
			return ref, i, true
		}
	}
	return HunkRef{}, 0, false
}

// hunkRow returns a hunk-body row and the hunk it belongs to.
func (d Document) hunkRow(row int) (HunkRef, Row, bool) {
	if row < 0 || row >= len(d.Rows) {
		return HunkRef{}, Row{}, false
	}
	r := d.Rows[row]
	if r.Kind != RowLine || r.Hunk < 0 || r.Hunk >= len(d.Hunks) {
		return HunkRef{}, Row{}, false
	}
	return d.Hunks[r.Hunk], r, true
}

// CommentAt returns the comment displayed on a row.
func (d Document) CommentAt(row int) (store.Comment, bool) {
	if row < 0 || row >= len(d.Rows) {
		return store.Comment{}, false
	}
	r := d.Rows[row]
	if r.Kind != RowComment || r.Comment < 0 || r.Comment >= len(d.Comments) {
		return store.Comment{}, false
	}
	return d.Comments[r.Comment], true
}

// AnchorOf returns the row a comment hangs off: the line it was written on, or
// the file header when it is a note on the file as a whole. Every other row is
// its own anchor.
func (d Document) AnchorOf(row int) int {
	if row < 0 || row >= len(d.Rows) {
		return row
	}
	for row > 0 && d.Rows[row].Kind == RowComment {
		row--
	}
	return row
}

// commentIndex places comments at the rows they anchor to, using each comment
// at most once so a line appearing on both the staged and unstaged side does
// not duplicate it.
type commentIndex struct {
	comments []store.Comment
	byFile   map[string][]int
	used     map[int]bool
}

func indexComments(cs []store.Comment) *commentIndex {
	idx := &commentIndex{comments: cs, byFile: map[string][]int{}, used: map[int]bool{}}
	for i, c := range cs {
		idx.byFile[c.File] = append(idx.byFile[c.File], i)
	}
	return idx
}

// takeFile returns the file-level comments for a path.
func (x *commentIndex) takeFile(path string) []int {
	return x.take(path, func(c store.Comment) bool { return c.Line <= 0 })
}

// takeLine returns comments anchored to either line of a displayed pair.
//
// A note whose code is gone claims no line at all. Its number still names a line
// — some line, whatever moved into the gap — and hanging the note there would be
// the failure the anchor exists to prevent, dressed up as a placement. It falls
// through to rest and is drawn under its file instead, saying so.
func (x *commentIndex) takeLine(ref HunkRef, pair linePair) []int {
	return x.take(ref.Path, func(c store.Comment) bool {
		if c.Line <= 0 || c.Outdated || !sameOrigin(c.Origin, ref) {
			return false
		}
		for _, i := range []int{pair.left, pair.right} {
			if i < 0 || i >= len(ref.Hunk.Lines) {
				continue
			}
			if anchors(ref.Hunk.Lines[i], c.Side, c.Line) {
				return true
			}
		}
		return false
	})
}

// sameOrigin reports that a note numbered against one of the two diffs belongs
// to this one.
//
// A file can be part staged and part modified, and then line 12 of the index is
// a different line from line 12 of the working tree — so a note that names its
// diff is only ever placed on that one. A note that does not name one is older
// than the distinction, or came from a caller that did not draw it, and goes
// where it always went: the first line that matches its number.
func sameOrigin(o store.Origin, ref HunkRef) bool {
	return o == "" || o == ref.Origin()
}

// rest returns the comments for a path that nothing claimed, so a comment whose
// line has since moved out of the diff still appears under its file.
func (x *commentIndex) rest(path string) []int {
	return x.take(path, func(store.Comment) bool { return true })
}

func (x *commentIndex) take(path string, match func(store.Comment) bool) []int {
	var out []int
	for _, i := range x.byFile[path] {
		if x.used[i] || !match(x.comments[i]) {
			continue
		}
		x.used[i] = true
		out = append(out, i)
	}
	return out
}

// anchors reports that a note on the given line number and side belongs to l.
func anchors(l git.Line, side store.Side, line int) bool {
	if side == store.SideOld {
		return l.OldLine == line
	}
	return l.NewLine == line
}
