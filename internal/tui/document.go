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
	// RowExpand stands where the diff leaves unchanged code out, and offers to
	// read it in.
	RowExpand
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
	// Expand indexes into Document.Expands on a row offering to read in code the
	// diff left out, and is -1 on every other row.
	Expand int
	// Noted marks a line a saved comment was written about — every line of the
	// run, not only the last one the note itself hangs off.
	Noted bool
	Text  string
	Head  bool
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
	// SectionShown reports that the line git named after the @@ has already been
	// said above — drawn as code an expansion read in, or named by the first
	// header of a run of hunks git named alike — so the header has nothing left
	// to add by repeating it.
	SectionShown bool
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
	// Orphan marks a file the session does not hold, drawn only because notes
	// were left on it before its changes went. It has no diff, so there is
	// nothing under it to stage and nothing but the notes to read.
	Orphan bool
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
	// Editing is the ID of the comment being rewritten, and empty when the note
	// is a new one. The editor then stands in that comment's place rather than
	// under it: what is being written is the note itself, and drawing both would
	// put two versions of it on screen at once.
	Editing string
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
	// pane is the room the diff has on screen, which is what a comment is
	// wrapped to. Zero leaves a comment's lines as they were written.
	pane int
	// expand is the unchanged code read in around the hunks, and the files it
	// comes out of. Its zero value offers nothing, which is what leaves a diff
	// peel has no copy of the files for exactly as git printed it.
	expand Expansion
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

// WithPaneWidth gives the layout the room the diff has, so a comment too long
// for it runs on to the next row instead of off the edge — wrapped to the half
// it hangs under, where the split layout hangs it under one.
func WithPaneWidth(w int) BuildOption {
	return func(c *buildConfig) { c.pane = w }
}

// WithExpansion draws in the unchanged code a review has asked to see around its
// hunks, and marks where there is more of it still hidden.
func WithExpansion(x Expansion) BuildOption {
	return func(c *buildConfig) { c.expand = x }
}

// Document is a session flattened into navigable rows.
type Document struct {
	Files []FileRef
	Hunks []HunkRef
	// Sides are the labelled halves of the part-staged files, in layout order.
	// A file whose changes are all in one place has none: there is nothing to
	// tell apart.
	Sides []SideRef
	// Expands are the places the diff leaves unchanged code out and offers to
	// read it in, in layout order.
	Expands []ExpandRef
	Rows    []Row
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
	// pane is the room the diff has on screen, which a comment is wrapped to
	// before its tag is taken off the first row.
	pane int
	// expand is the code read in around the hunks, kept so the layout can be
	// worked out one side at a time.
	expand Expansion
}

// Build flattens a session into rows. collapsed hides a file's body by path.
func Build(s *app.Session, comments []store.Comment, collapsed map[string]bool, layout Layout, opts ...BuildOption) Document {
	var cfg buildConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	doc := Document{Comments: comments, Layout: layout, Draft: cfg.draft, DraftRow: -1,
		pane: cfg.pane, expand: cfg.expand}
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
			doc.add(Row{Kind: RowFile, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1})
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
			doc.add(Row{Kind: RowBlank, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1})
		}
	}
	doc.addOrphans(s, idx, collapsed)
	return doc
}

// addOrphans draws the notes whose file has left the diff.
//
// A file's changes can be committed, stashed or put back while a note written on
// them is still in the review, and the note is then anchored to a file the
// session does not hold: no line to hang off, no hunk, not even a header. It was
// drawn nowhere at all — while the store kept it, `C` handed it to an agent, and
// the file changing again brought it back onto whatever had taken its number.
//
// So the file is drawn anyway, saying where its changes went in place of the
// diff it no longer has, with its notes beneath it. What becomes of them is then
// the reviewer's to decide, which is the one thing a note drawn nowhere never
// let them do.
func (d *Document) addOrphans(s *app.Session, idx *commentIndex, collapsed map[string]bool) {
	inDiff := make(map[string]bool, len(s.Files))
	for _, f := range s.Files {
		inDiff[f.Path] = true
	}
	for _, c := range d.Comments {
		if inDiff[c.File] {
			continue
		}
		inDiff[c.File] = true

		fi := len(d.Files)
		hidden := collapsed[c.File]
		notes := idx.rest(c.File)
		d.Files = append(d.Files, FileRef{Entry: git.FileEntry{Path: c.File},
			Row: len(d.Rows), Collapsed: hidden, Orphan: true})
		d.add(Row{Kind: RowFile, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1})
		if !hidden {
			d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1,
				Text: orphanNote(len(notes))})
		}
		// The notes stay on screen whether or not the file is folded away, as
		// they do on a file the diff still holds: a folded note is a note back
		// where it was, drawn nowhere.
		d.addComments(fi, -1, notes)
		d.addDraft(fi, -1, d.Draft.path == c.File)
		d.add(Row{Kind: RowBlank, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1})
	}
}

// orphanNote stands where the diff would be on a file that has none left, naming
// the two ways a change goes without the notes on it going too.
func orphanNote(n int) string {
	left := "leaving these notes behind"
	if n == 1 {
		left = "leaving this note behind"
	}
	return "no changes here any more — committed, or put back — " + left
}

// orphanLabel stands in for the line counts on a file that has no diff to count.
const orphanLabel = "not in this change"

// orphanPaths names the files drawn only because notes were left on them, for
// the places that have to say so in words rather than in a header.
func (d Document) orphanPaths() map[string]bool {
	out := map[string]bool{}
	for _, f := range d.Files {
		if f.Orphan {
			out[f.Entry.Path] = true
		}
	}
	return out
}

// addDraft lays the editor out here, if this is where the comment being written
// belongs and it has not already been placed.
func (d *Document) addDraft(file, hunk int, here bool) {
	if !here || d.Draft.Height <= 0 || d.DraftRow >= 0 {
		return
	}
	d.DraftRow = len(d.Rows)
	for range d.Draft.Height {
		d.add(Row{Kind: RowDraft, File: file, Hunk: hunk, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1})
	}
}

// editorOn reports that the editor is open on this comment, which is drawn as
// the editor standing where it stands: a note being rewritten is one note, and
// its old text under the editor holding the new would read as two.
func (d Document) editorOn(c store.Comment) bool {
	return d.Draft.Editing != "" && d.Draft.Editing == c.ID && d.Draft.Height > 0 && d.DraftRow < 0
}

// draftPlacedByAnchor reports that where the editor goes is worked out from the
// anchor the note will attach to.
//
// Every draft but one: an editor opened on a note already in the review goes
// where that note is drawn instead, so a note peel could not place — one whose
// code has been rewritten away — is still rewritten where the reviewer found it,
// rather than on whatever line has since taken its number.
func (d Document) draftPlacedByAnchor() bool { return d.Draft.Editing == "" }

// draftOnFile reports that the comment being written is a note on the file as a
// whole, which is where a draft with no line of its own goes.
func (d Document) draftOnFile(path string) bool {
	return d.draftPlacedByAnchor() && d.Draft.path == path && d.Draft.line <= 0 && d.Draft.hunk == ""
}

// draftOnHunk reports that the comment being written is about a hunk that has no
// line to hang it on.
func (d Document) draftOnHunk(ref HunkRef) bool {
	return d.draftPlacedByAnchor() && d.Draft.path == ref.Path && d.Draft.line <= 0 &&
		d.Draft.hunk == ref.ID.String()
}

// draftOnLine reports that the comment being written hangs off either line of a
// displayed pair.
func (d Document) draftOnLine(ref HunkRef, pair linePair) bool {
	if !d.draftPlacedByAnchor() {
		return false
	}
	if d.Draft.path != ref.Path || d.Draft.line <= 0 || !sameOrigin(d.Draft.origin, ref) {
		return false
	}
	return pairHolds(ref, pair, d.Draft.side, hangsOn(d.Draft.line, d.Draft.end))
}

// hangsOn is the line a note hangs off: the last line of the run it covers, so
// the note sits under the whole of what it is about rather than partway down it,
// with the rest of the run reading as code nobody wrote about.
//
// A note on a single line is a run of one, and hangs off that line.
func hangsOn(line, end int) int {
	if end > line {
		return end
	}
	return line
}

// pairHolds reports that a displayed pair shows the given line of the given
// side.
func pairHolds(ref HunkRef, pair linePair, side store.Side, line int) bool {
	for _, i := range []int{pair.left, pair.right} {
		if i < 0 || i >= len(ref.Hunk.Lines) {
			continue
		}
		if anchors(ref.Hunk.Lines[i], side, line) {
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
	row := Row{Kind: RowStep, File: file, Hunk: -1, Left: -1, Right: -1, Step: index, Side: -1, Expand: -1}
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
		if d.editorOn(d.Comments[ci]) {
			d.addDraft(file, hunk, true)
			continue
		}
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
		for _, text := range d.wrapComment(d.Comments[ci], hunk >= 0) {
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
// on the first row, and the renderer indents the rest below it to match. online
// says the note hangs off a line of a hunk rather than off a file, which is what
// decides whether the split layout gives it half the pane or all of it.
func (d Document) wrapComment(c store.Comment, online bool) []string {
	lines := strings.Split(c.Body, "\n")
	if d.pane <= 0 {
		return lines
	}
	room := noteHalf(d.Layout, online, c.Side).room(d.pane)
	width := max(room-ansi.StringWidth(commentTag(c)), minCommentWidth)
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
		d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1,
			Text: "binary file — no diff to show"})
		return
	}

	// Only a file git holds in both places at once has two halves to tell apart.
	// A file whose changes are all in one place — everything still in the working
	// tree, or everything already staged — is left as the plain run of hunks it
	// has always been: there is nothing to tell it from, and a heading saying
	// "staged" over a file whose header already says so twice adds a line to read
	// and nothing to read it for.
	labelled, folded := entry.State() == git.StatePartial, false
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
		d.add(Row{Kind: RowNote, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1,
			Text: emptyNote(entry)})
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
	d.add(Row{Kind: RowSide, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: si, Expand: -1})
	return si
}

// sideFolded reports whether a side opens hidden.
//
// The index's half does: it has been reviewed and put away already, so what is
// left open is what is left to read — the same rule staging a whole file
// follows. `space` on the heading has the last word.
//
// Only a part-staged file gets here, so hiding the index's half always leaves
// the other one on screen; a fold that emptied the file would be a header
// pointing at nothing.
func sideFolded(e git.FileEntry, s side, folds map[string]bool) bool {
	if !s.staged {
		return false
	}
	if folded, set := folds[e.Path]; set {
		return folded
	}
	return true
}

// addHunks lays out one side's hunks, each carrying whatever unchanged code has
// been read in above and below it.
//
// A hunk's revealed lines are part of the hunk rather than rows beside it, so
// the cursor walks them, a note anchors to them, and the split layout pairs them
// like any other context. What is still hidden is marked instead: a row under
// the hunk above the run, a row over the hunk below it, and `space` on either
// opens that end.
func (d *Document) addHunks(fi int, entry git.FileEntry, s side, si int, idx *commentIndex) {
	gaps := gapsOf(FileSide{Path: entry.Path, Staged: s.staged}, s.diff.Hunks, s.diff.ID, d.expand)
	count := len(s.diff.Hunks)

	// above is every line this side has drawn before the hunk being laid out. It
	// runs across the gaps: a run still holding code back leaves the declaration
	// further off, but it does not take it back off the screen.
	var above []git.Line
	// said holds the sections a header has already named. A run of hunks inside
	// one declaration is named after it every time, so the same words would come
	// down the screen once per hunk; the first header answers what the change
	// sits inside, and the ones under it repeat an answer already read.
	said := map[string]bool{}
	for i, h := range s.diff.Hunks {
		hi := len(d.Hunks)
		shown := h
		shown.Lines = withContext(gaps.bottom(i), h.Lines, gaps.top(i+1))
		d.Hunks = append(d.Hunks, HunkRef{
			File:         fi,
			Path:         entry.Path,
			Staged:       s.staged,
			ID:           s.diff.ID(h),
			Hunk:         shown,
			SectionShown: said[h.Section] || sectionShown(h.Section, above, gaps.bottom(i)),
		})
		if h.Section != "" {
			said[h.Section] = true
		}
		above = append(above, shown.Lines...)
		d.Files[fi].Hunks = append(d.Files[fi].Hunks, hi)
		if si >= 0 {
			d.Sides[si].Hunks = append(d.Sides[si].Hunks, hi)
		}
		d.add(Row{Kind: RowHunk, File: fi, Hunk: hi, Left: -1, Right: -1, Step: -1, Side: -1, Expand: -1})
		d.addDraft(fi, hi, d.draftOnHunk(d.Hunks[hi]))
		d.measure(shown.Lines)

		// The run above this hunk is marked under its header rather than over it:
		// the lines it opens arrive directly below, so the row stands where the
		// code it is about to show will be.
		d.addExpand(fi, gaps, i, count, gaps.above)
		for _, pair := range pairLines(shown.Lines, d.Layout) {
			d.add(Row{Kind: RowLine, File: fi, Hunk: hi, Left: pair.left, Right: pair.right, Step: -1, Side: -1, Expand: -1,
				Noted: idx.covers(d.Hunks[hi], pair)})
			d.addComments(fi, hi, idx.takeLine(d.Hunks[hi], pair))
			d.addDraft(fi, hi, d.draftOnLine(d.Hunks[hi], pair))
		}
		d.addExpand(fi, gaps, i+1, count, gaps.below)
	}
}

// sectionShown reports that the line git named after a hunk's @@ is one of the
// lines already on screen.
//
// git prints that line because the diff cuts the file off above the hunk and the
// reviewer cannot see what encloses it. Once a reveal has read it back in — code
// above the hunk opened down to it, or the hunk's own head read further back —
// the header would be naming a line drawn a few rows away, and the file says it
// better than the header does. Code still hidden between the two does not put it
// back out of sight: the reviewer has read the declaration either way.
//
// The lines are matched by their text: the @@ carries no number for its section,
// only the words, and a funcname driver hands them back with the indent dropped
// and cut off at the end of what its pattern matched.
func sectionShown(section string, runs ...[]git.Line) bool {
	if section == "" {
		return false
	}
	for _, run := range runs {
		for _, l := range run {
			if strings.HasPrefix(strings.TrimSpace(l.Text), section) {
				return true
			}
		}
	}
	return false
}

// addExpand marks one end of a run of hidden lines, when that end has a row.
//
// where picks the end: gaps.above for the row over the hunk below the run,
// gaps.below for the one under the hunk above it.
func (d *Document) addExpand(fi int, g gaps, i, hunks int, where func(int, int) (ExpandDir, bool)) {
	dir, ok := where(i, hunks)
	if !ok {
		return
	}
	ei := len(d.Expands)
	d.Expands = append(d.Expands, ExpandRef{
		ExpandKey: g.keyOf(i, dir),
		File:      fi,
		Dir:       dir,
		Hidden:    g.hidden(i),
		Row:       len(d.Rows),
	})
	d.add(Row{Kind: RowExpand, File: fi, Hunk: -1, Left: -1, Right: -1, Step: -1, Side: -1, Expand: ei})
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
//
// A row offering to read in hidden code is not one of them. It sits against the
// hunk it extends, one arrow key away, and stopping on it would put two extra
// presses between every pair of hunks in the pass.
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

// Leap returns where a jump of n lines from row lands: n cursor positions down
// the document for a positive n and up it for a negative one, or the first row
// that ends a leap on the way there.
//
// It counts cursor positions rather than rows, so a jump of ten is at most ten
// presses of the arrow and lands where they would: the blank between two files,
// a folded step's explanation and the lines a comment runs on to are passed over
// without being counted, since the cursor never rests on one.
//
// The ends of the document stop it the same way a heading does: past them there
// is nothing left to count, and it returns the last position it reached.
func (d Document) Leap(row, n int) int {
	step := d.NextStop
	if n < 0 {
		step, n = d.PrevStop, -n
	}
	for range n {
		next := step(row)
		if next == row {
			break
		}
		row = next
		if d.endsLeap(row) {
			break
		}
	}
	return row
}

// endsLeap reports that a row stops a leap short of its full distance: anything
// that heads what comes under it — a file, one of the two halves of a file git
// holds in both places at once, a walkthrough group — or a row standing where
// the diff left code out.
//
// They are the places the reading changes rather than continues, and the ten
// lines are worth less than arriving at one of them. A leap that ran past a
// heading would land under something whose name went by on the way, which is
// where a review goes wrong quietly: the index's half of a file read as the
// working tree's is a line number pointing at the wrong line. One that ran past
// hidden code would put the reviewer below a gap they were given a row to open,
// the row being what says the code the jump crossed was never shown. Landing on
// any of them leaves the next press to carry on past it, which is the same jump
// split where something happened.
func (d Document) endsLeap(row int) bool {
	if row < 0 || row >= len(d.Rows) {
		return false
	}
	switch d.Rows[row].Kind {
	case RowFile, RowSide, RowStep, RowExpand:
		return true
	default:
		return false
	}
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

// ExpandAt returns the run of hidden lines a row offers to open, and -1 on
// every other row.
func (d Document) ExpandAt(row int) int {
	if row < 0 || row >= len(d.Rows) {
		return -1
	}
	at := d.Rows[row].Expand
	if at < 0 || at >= len(d.Expands) {
		return -1
	}
	return at
}

// RowOfExpand returns the row opening a run of hidden lines from the given end.
//
// It falls back to the run's other end, since opening one end can leave too
// little hidden to be worth two rows and the pair becomes the single row that
// finishes the run — which is still the row the reviewer was on.
func (d Document) RowOfExpand(key ExpandKey, dir ExpandDir) int {
	other := -1
	for _, e := range d.Expands {
		if e.ExpandKey != key {
			continue
		}
		if e.Dir == dir {
			return e.Row
		}
		other = e.Row
	}
	return other
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

// LineRows returns the rows displaying a hunk's body, in reading order.
//
// A run of marked lines is held as positions in this list rather than as row
// numbers: laying the document out again — opening the comment editor inside the
// run, or resizing the pane — renumbers every row, while a hunk's body stays
// exactly as long and in exactly the same order.
func (d Document) LineRows(hunk int) []int {
	if hunk < 0 || hunk >= len(d.Hunks) {
		return nil
	}
	var out []int
	for i, r := range d.Rows {
		if r.Kind == RowLine && r.Hunk == hunk {
			out = append(out, i)
		}
	}
	return out
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

// StackedAround returns the IDs of the notes drawn immediately under and over
// the one at row, within the unbroken run of notes it sits in. Either is empty
// where the run ends.
//
// It is what a note that hangs off no line falls back to when it goes: those are
// drawn under their file, one after another with no code between them, so the
// note beside it in the run is the nearest thing it was next to.
func (d Document) StackedAround(row int) (under, over string) {
	c, ok := d.CommentAt(row)
	if !ok {
		return "", ""
	}
	start := row
	for start > 0 && d.Rows[start-1].Kind == RowComment {
		start--
	}
	var run []string
	for i := start; i < len(d.Rows) && d.Rows[i].Kind == RowComment; i++ {
		r := d.Rows[i]
		if r.Head && r.Comment >= 0 && r.Comment < len(d.Comments) {
			run = append(run, d.Comments[r.Comment].ID)
		}
	}
	for i, id := range run {
		if id != c.ID {
			continue
		}
		if i+1 < len(run) {
			under = run[i+1]
		}
		if i > 0 {
			over = run[i-1]
		}
		return under, over
	}
	return "", ""
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

// takeLine returns the comments that hang off either line of a displayed pair.
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
		return pairHolds(ref, pair, c.Side, hangsOn(c.Line, c.EndLine))
	})
}

// covers reports that some saved note was written about this displayed pair.
//
// It looks at the whole of every note's run rather than the line the note hangs
// off, so the reviewer can see how far a note reaches while reading the code —
// the tag under the run says where it starts, but only once you have got there.
// Nothing is claimed here: a note bars all of its lines and is still drawn once,
// at its anchor.
func (x *commentIndex) covers(ref HunkRef, pair linePair) bool {
	for _, i := range x.byFile[ref.Path] {
		c := x.comments[i]
		if c.Line <= 0 || c.Outdated || !sameOrigin(c.Origin, ref) {
			continue
		}
		if pairWithin(ref, pair, c) {
			return true
		}
	}
	return false
}

// pairWithin reports that a displayed pair shows a line inside a note's run.
//
// A run of one is a note on a single line, and that line is inside it.
func pairWithin(ref HunkRef, pair linePair, c store.Comment) bool {
	for _, i := range []int{pair.left, pair.right} {
		if i < 0 || i >= len(ref.Hunk.Lines) {
			continue
		}
		n := lineOn(ref.Hunk.Lines[i], c.Side)
		if n >= c.Line && n <= hangsOn(c.Line, c.EndLine) {
			return true
		}
	}
	return false
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
	return lineOn(l, side) == line
}

// lineOn is a line's number on one side of the diff, and 0 where that side does
// not hold it at all.
func lineOn(l git.Line, side store.Side) int {
	if side == store.SideOld {
		return l.OldLine
	}
	return l.NewLine
}
