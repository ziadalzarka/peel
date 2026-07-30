package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// mode is which of the UI's screens has the keyboard.
type mode int

const (
	modeBrowse mode = iota
	modeComment
	modeHelp
)

// Model is the review UI's state.
//
// Everything it needs from the outside arrives through Backend, so Update can be
// driven key by key in a test with no terminal and no repository.
type Model struct {
	ctx     context.Context
	backend Backend

	session   *app.Session
	comments  []store.Comment
	doc       Document
	collapsed map[string]bool

	cursor int
	top    int
	// fileTop is the first file drawn in the side pane. It scrolls
	// independently of the diff, and only follows the diff when the marked file
	// has left the pane.
	fileTop int

	mode   mode
	layout Layout

	width  int
	height int

	theme    Theme
	renderer *Renderer

	input   textarea.Model
	pending anchor

	// walkSteps is the narrative parsed into the groups it comments on, kept so
	// the diff can be laid out again when the terminal resizes or a step folds.
	walkSteps []store.Step
	// walkLoaded reports that a narrative has been fetched, so opening the
	// walkthrough again does not pay a provider for one twice.
	walkLoaded bool
	// walkOn lays the diff out under the walkthrough's groups. Turning it off
	// puts the files back in git's order with no commentary between them.
	walkOn bool
	// walkFolded hides a step's explanation, by step index.
	walkFolded map[int]bool
	// walkCode identifies the code the narrative was written about: git diff
	// HEAD, which reads the same whether a change is staged or not, so staging a
	// hunk does not date the narrative.
	walkCode string
	// walkStale reports that the code moved on under the narrative, so what it
	// says may no longer be true and `W` is worth pressing.
	walkStale bool

	// follow re-reads the repository on a timer, for watching a build or an
	// agent change files while reviewing.
	follow bool
	// pollEvery is how often follow mode checks. Zero means the default.
	pollEvery time.Duration
	// fingerprint identifies the diff currently on screen, so a poll that finds
	// nothing new can be dropped instead of redrawing under the reviewer.
	fingerprint string

	busy   string
	status string
	err    error

	quitting bool
}

// defaultPollEvery is how often follow mode re-reads the repository. Long
// enough that a large repository is not re-diffed constantly, short enough that
// a build finishing feels immediate.
const defaultPollEvery = 2 * time.Second

// options are the knobs New accepts.
type options struct {
	theme     Theme
	layout    Layout
	syntax    *Highlighter
	width     int
	height    int
	follow    bool
	pollEvery time.Duration
	provider  string
}

// Option customises a Model.
type Option func(*options)

// WithTheme replaces the colour scheme.
func WithTheme(t Theme) Option { return func(o *options) { o.theme = t } }

// WithLayout sets the initial diff layout.
func WithLayout(l Layout) Option { return func(o *options) { o.layout = l } }

// WithoutSyntax turns off colouring by language, which tests rely on so
// rendered rows are comparable as plain text.
func WithoutSyntax() Option { return func(o *options) { o.syntax = nil } }

// WithSize sets the initial terminal size, before the first resize arrives.
func WithSize(w, h int) Option {
	return func(o *options) { o.width, o.height = w, h }
}

// WithFollow starts in follow mode, re-reading the repository on a timer.
func WithFollow(on bool) Option { return func(o *options) { o.follow = on } }

// WithProvider selects the AI provider used for the walkthrough.
func WithProvider(name string) Option { return func(o *options) { o.provider = name } }

// WithPollInterval sets how often follow mode re-reads the repository.
func WithPollInterval(d time.Duration) Option {
	return func(o *options) { o.pollEvery = d }
}

// New builds the review UI for a session and the comments already on it.
func New(ctx context.Context, backend Backend, session *app.Session, comments []store.Comment, opts ...Option) *Model {
	cfg := options{theme: DefaultTheme(), syntax: NewHighlighter(), width: 100, height: 30}
	for _, opt := range opts {
		opt(&cfg)
	}

	m := &Model{
		ctx:        ctx,
		backend:    backend,
		session:    session,
		comments:   comments,
		collapsed:  map[string]bool{},
		layout:     cfg.layout,
		theme:      cfg.theme,
		renderer:   NewRenderer(cfg.theme, cfg.syntax),
		input:      newInput(cfg.theme),
		walkFolded: map[int]bool{},
		follow:     cfg.follow,
		pollEvery:  cfg.pollEvery,
	}
	if m.pollEvery <= 0 {
		m.pollEvery = defaultPollEvery
	}
	m.fingerprint = fingerprintOf(session)
	m.resize(cfg.width, cfg.height)
	m.rebuild()
	m.cursor = m.doc.FirstStop()
	return m
}

// draftMinHeight and draftMaxHeight bound the inline editor. It opens small, so
// it barely parts the diff, and grows a row per line written until it would take
// the pane over — past that it scrolls inside itself.
const (
	draftMinHeight = 3
	draftMaxHeight = 12
)

func newInput(theme Theme) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Write a review comment…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
	// The editor draws in the comment's own bar and colour, so a note being
	// written reads as the note it is about to become. The line being typed on
	// takes the same colour rather than the library's highlight band, which would
	// sit as a block of background across a diff that now tints its own lines.
	ta.Prompt = "┃ "
	// Enter saves, since most comments are one line and reaching for a chord to
	// finish one is the wrong default. Writing a second line is the deliberate
	// press.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter"),
		key.WithHelp("alt+enter", "new line"),
	)
	for _, style := range []*textarea.Style{&ta.FocusedStyle, &ta.BlurredStyle} {
		style.Prompt = theme.Comment
		style.Text = theme.Comment
		style.CursorLine = theme.Comment
		style.Placeholder = theme.Note
	}
	ta.SetHeight(draftMinHeight)
	return ta
}

// Init satisfies tea.Model. The session and its comments are already loaded, so
// the only thing to start is the follow timer.
func (m *Model) Init() tea.Cmd {
	if m.follow {
		return m.tickCmd()
	}
	return nil
}

// Update satisfies tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case loadedMsg:
		m.applyLoaded(msg)
		return m, nil
	case walkthroughMsg:
		m.busy = ""
		m.showWalkthrough(msg.body)
		return m, nil
	case errMsg:
		m.busy = ""
		m.err = msg.err
		return m, nil
	case tickMsg:
		if !m.follow {
			return m, nil
		}
		return m, tea.Batch(m.pollCmd(), m.tickCmd())
	case tea.MouseMsg:
		return m, m.mouse(msg)
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

// wheelLines is how far one notch of the wheel scrolls.
const wheelLines = 3

// mouse routes a wheel notch to whichever pane the pointer is over, so the file
// list and the diff scroll separately.
func (m *Model) mouse(msg tea.MouseMsg) tea.Cmd {
	if m.mode == modeComment || m.mode == modeHelp {
		return nil
	}

	var delta int
	switch msg.Button {
	case tea.MouseButtonWheelDown:
		delta = wheelLines
	case tea.MouseButtonWheelUp:
		delta = -wheelLines
	default:
		return nil
	}
	if pane := m.filePaneWidth(); pane > 0 && msg.X < pane {
		m.scrollFiles(delta)
		return nil
	}
	m.scrollDiff(delta)
	return nil
}

// key routes a press to whichever screen has the keyboard.
func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	switch m.mode {
	case modeHelp:
		return m.helpKey(msg)
	case modeComment:
		return m.commentKey(msg)
	default:
		return m.browseKey(msg)
	}
}

func (m *Model) browseKey(msg tea.KeyMsg) tea.Cmd {
	m.status, m.err = "", nil

	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return tea.Quit
	case "?":
		m.mode = modeHelp
	case "j":
		m.moveTo(m.doc.NextMark(m.cursor))
	case "k":
		m.moveTo(m.doc.PrevMark(m.cursor))
	case "down":
		m.moveTo(m.doc.NextStop(m.cursor))
	case "up":
		m.moveTo(m.doc.PrevStop(m.cursor))
	case "]":
		m.scrollFiles(1)
	case "[":
		m.scrollFiles(-1)
	case "J", "}":
		m.showFile(m.doc.NextFile(m.cursor))
	case "K", "{":
		m.showFile(m.doc.PrevFile(m.cursor))
	case "g", "home":
		m.moveTo(m.doc.FirstStop())
	case "G", "end":
		m.moveTo(m.doc.LastStop())
	case "ctrl+d", "pgdown":
		m.moveTo(m.doc.Nearest(m.cursor + m.bodyHeight()/2))
	case "ctrl+u", "pgup":
		m.moveTo(m.doc.Nearest(m.cursor - m.bodyHeight()/2))
	case "tab":
		m.toggleCollapse()
	case "s":
		return m.stageAt()
	case "u":
		return m.unstageAt()
	case "a":
		return m.runCollapsing("staging everything", "staged everything", m.foldEvery(true), m.backend.StageAll)
	case "U":
		return m.runCollapsing("unstaging everything", "unstaged everything", m.foldEvery(false), m.backend.UnstageAll)
	case "c":
		m.openComment()
	case "x":
		return m.toggleResolved()
	case "D":
		return m.deleteComment()
	case `\`:
		m.setLayout(m.layout.Toggle())
	case "w":
		return m.toggleWalkthrough()
	case "W":
		return m.walkCmd(true)
	case "r":
		return m.reload("reloaded")
	case "f":
		return m.toggleFollow()
	}
	return nil
}

func (m *Model) commentKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.closeComment()
		m.status = "comment cancelled"
		return nil
	case "enter":
		return m.submitComment()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.fitDraft()
	return cmd
}

// fitDraft sizes the editor to what has been written, so the diff parts by
// exactly as much as the comment needs and closes back up as lines are deleted.
func (m *Model) fitDraft() {
	want := min(max(m.input.LineCount(), draftMinHeight), draftMaxHeight)
	if want == m.input.Height() {
		return
	}
	m.input.SetHeight(want)
	m.relayout()
	m.revealDraft()
}

// groups is the walkthrough the document is laid out under. It stays empty until
// a narrative has been fetched and the reviewer has it on, and an empty grouping
// is what leaves the diff in git's order with nothing between the files.
func (m *Model) groups() Groups {
	if !m.walkOn || !m.walkLoaded {
		return Groups{}
	}
	return Groups{Steps: m.walkSteps, Folded: m.walkFolded, Width: m.walkWidth()}
}

// walkWidth is the room a step's explanation is wrapped to, leaving the indent
// the renderer puts in front of it.
func (m *Model) walkWidth() int { return max(m.diffWidth()-stepIndent-1, 20) }

// showWalkthrough lays the diff out under a freshly generated narrative.
func (m *Model) showWalkthrough(body string) {
	m.walkSteps = store.ParseSteps(body)
	m.walkLoaded = true
	m.walkOn = true
	m.walkFolded = map[int]bool{}
	m.walkCode, m.walkStale = codeFingerprintOf(m.session), false
	m.relayout()
	if len(m.walkSteps) == 0 {
		m.status = "the walkthrough came back empty"
	}
}

// toggleWalkthrough shows or hides the walkthrough's grouping, fetching the
// narrative the first time it is asked for.
func (m *Model) toggleWalkthrough() tea.Cmd {
	if m.walkOn {
		m.walkOn = false
		m.relayout()
		m.status = "walkthrough hidden"
		return nil
	}
	if !m.walkLoaded {
		return m.walkCmd(false)
	}
	m.walkOn = true
	m.relayout()
	return nil
}

// spot identifies what the cursor is on in a way that survives a rebuild, since
// rebuilding renumbers every row.
type spot struct {
	path string
	// comment is the ID of the comment the cursor is on, when it is on one.
	comment string
	hunk    git.HunkID
	// line indexes into the hunk's lines, and is -1 when the cursor is not on
	// one. Hunk IDs carry the hunk header, so a hunk that kept its ID kept its
	// lines too and the index still names the same code.
	line int
}

// spot records where the cursor is, so a rebuild can put it back.
func (m *Model) spot() spot {
	at := spot{path: m.currentPath(), line: -1}
	if c, ok := m.doc.CommentAt(m.cursor); ok {
		at.comment = c.ID
		return at
	}
	switch target := m.doc.TargetAt(m.cursor); target.Kind {
	case TargetHunk:
		at.hunk = m.doc.Hunks[target.Hunk].ID
	case TargetLine:
		if ref, index, ok := m.doc.LineAt(m.cursor); ok {
			at.hunk, at.line = ref.ID, index
		}
	}
	return at
}

// moveToSpot puts the cursor back on what spot named, falling back through the
// hunk and then the file when the exact line has gone.
func (m *Model) moveToSpot(at spot) {
	if row := m.doc.RowOfComment(at.comment); row >= 0 {
		m.moveTo(row)
		return
	}
	hunk := m.findHunk(at.hunk)
	if row := m.doc.RowOfLine(hunk, at.line); row >= 0 {
		m.moveTo(row)
		return
	}
	if row := m.doc.RowOfHunk(hunk); row >= 0 {
		m.moveTo(row)
		return
	}
	if row := m.doc.RowOfFile(m.fileIndex(at.path)); row >= 0 {
		m.moveTo(row)
		return
	}
	m.moveTo(m.doc.Nearest(min(m.cursor, max(m.doc.Len()-1, 0))))
}

// relayout rebuilds after something that renumbers every row — switching the
// hunk layout, grouping the files under a walkthrough, or opening the comment
// editor — and puts the cursor back on the code it was on rather than leaving it
// at the same row index.
func (m *Model) relayout() {
	at := m.spot()
	m.rebuild()
	m.moveToSpot(at)
}

// foldStep hides the explanation of the step at the cursor, so a walkthrough
// that has been read stops taking room from the diff it describes.
func (m *Model) foldStep(step int) {
	if m.walkFolded[step] {
		delete(m.walkFolded, step)
	} else {
		m.walkFolded[step] = true
	}
	m.rebuild()
	if step < len(m.doc.Steps) {
		m.moveTo(m.doc.Steps[step].Row)
	}
}

func (m *Model) helpKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return tea.Quit
	}
	m.mode = modeBrowse
	return nil
}

// stageAt stages the file the cursor is in.
//
// A file is the smallest thing that can be staged, so a hunk header or a diff
// line stages the file it belongs to. The file folds away once it is staged and
// the cursor moves on to the next one still to review, since this one has been
// dealt with — `tab` opens it again.
func (m *Model) stageAt() tea.Cmd {
	file, ok := m.doc.FileTargetAt(m.cursor)
	if !ok {
		m.status = "nothing to stage here"
		return nil
	}
	path := file.Entry.Path
	if file.Entry.State() == git.StateStaged {
		// Nothing to write, but `s` still means "done with this one" — a file
		// reopened with `tab` folds again, and moves on, without a pointless git
		// call.
		m.status = path + " is already staged"
		m.foldNow(path)
		return nil
	}
	return m.runAdvancing("staging "+path, "staged "+path, path, func(ctx context.Context) error {
		return m.backend.StageFile(ctx, path)
	})
}

// unstageAt takes the file the cursor is in back out of the index, and opens it
// again — it is back to being something to review.
func (m *Model) unstageAt() tea.Cmd {
	file, ok := m.doc.FileTargetAt(m.cursor)
	if !ok {
		m.status = "nothing to unstage here"
		return nil
	}
	path := file.Entry.Path
	if file.Entry.Staged == nil {
		m.status = path + " has nothing staged"
		return nil
	}
	return m.runCollapsing("unstaging "+path, "unstaged "+path, unfold(path), func(ctx context.Context) error {
		return m.backend.UnstageFile(ctx, path)
	})
}

// fold and unfold name the two one-file collapse changes staging makes.
func fold(path string) map[string]bool   { return map[string]bool{path: true} }
func unfold(path string) map[string]bool { return map[string]bool{path: false} }

// setCollapsed folds the named files away, or opens the ones marked false.
func (m *Model) setCollapsed(collapse map[string]bool) {
	for path, hide := range collapse {
		if hide {
			m.collapsed[path] = true
		} else {
			delete(m.collapsed, path)
		}
	}
}

// foldNow folds a file away immediately, for the case that never reaches git,
// and moves on the way staging does.
func (m *Model) foldNow(path string) {
	m.setCollapsed(fold(path))
	m.rebuild()
	m.advanceFrom(path)
}

// advanceFrom moves the cursor on to the next file with something left to
// review, which is where the pass carries on once path has been dealt with. It
// stays on path when nothing below it is left, since a file that has just been
// folded is a better place to be than an arbitrary one.
func (m *Model) advanceFrom(path string) {
	if next := m.nextToReview(path); next >= 0 {
		m.showFile(m.doc.topOf(m.doc.RowOfFile(next)))
		return
	}
	m.restoreCursor(path)
}

// nextToReview finds the first file below path that is not already staged, or
// -1 when there is none — including when path itself has gone.
func (m *Model) nextToReview(path string) int {
	file := m.fileIndex(path)
	if file < 0 {
		return -1
	}
	for i := file + 1; i < len(m.doc.Files); i++ {
		if m.doc.Files[i].Entry.State() != git.StateStaged {
			return i
		}
	}
	return -1
}

// foldEvery collapses or opens every file on screen, for the whole-tree
// operations, so `a` leaves the same folded list one file at a time would.
func (m *Model) foldEvery(collapsed bool) map[string]bool {
	out := make(map[string]bool, len(m.doc.Files))
	for _, f := range m.doc.Files {
		out[f.Entry.Path] = collapsed
	}
	return out
}

// findHunk locates a hunk by ID after a rebuild, since rebuilding renumbers the
// document's hunk indexes.
func (m *Model) findHunk(id git.HunkID) int {
	for i, ref := range m.doc.Hunks {
		if ref.ID == id {
			return i
		}
	}
	return -1
}

// anchor is where a new comment will attach.
type anchor struct {
	path string
	line int
	side store.Side
	hunk string
}

func (a anchor) location() string {
	if a.line <= 0 {
		return a.path
	}
	return fmt.Sprintf("%s:%d", a.path, a.line)
}

// openComment opens the editor where the comment will land, rather than on a
// screen of its own: what is being commented on stays in front of the reviewer
// while they write about it.
func (m *Model) openComment() {
	got, ok := m.anchorAt()
	if !ok {
		m.status = "nothing to comment on here"
		return
	}
	m.pending = got
	m.mode = modeComment
	m.input.Reset()
	m.input.SetWidth(m.draftWidth())
	m.input.SetHeight(draftMinHeight)
	m.input.Focus()
	m.status = "commenting on " + got.location()

	m.relayout()
	m.revealDraft()
}

// closeComment puts the editor away, leaving the cursor on the code it was
// opened from.
func (m *Model) closeComment() {
	m.input.Blur()
	m.mode = modeBrowse
	m.relayout()
}

// draft is the comment being written, or the zero draft when nothing is.
func (m *Model) draft() Draft {
	if m.mode != modeComment {
		return Draft{}
	}
	return Draft{anchor: m.pending, Height: m.input.Height()}
}

// draftWidth is the room the editor has, leaving the indent that lines it up
// with the comment bar.
func (m *Model) draftWidth() int { return max(m.diffWidth()-commentIndent, 20) }

// revealDraft scrolls the editor into view without taking the cursor off the
// code the comment is about.
func (m *Model) revealDraft() {
	if m.doc.DraftRow < 0 {
		return
	}
	m.ensureVisible(m.doc.DraftRow + m.doc.Draft.Height - 1)
	m.ensureFileVisible()
}

// draftLines is the editor as the rows that show it, one line each.
func (m *Model) draftLines() []string {
	if m.doc.DraftRow < 0 {
		return nil
	}
	return strings.Split(m.input.View(), "\n")
}

// anchorAt resolves the cursor to a comment anchor: the line under the cursor,
// the first changed line of a hunk, an existing comment's own anchor, or the file
// as a whole.
//
// A line anchor does not care whether the line changed, so a note can be left on
// the untouched code a change breaks.
func (m *Model) anchorAt() (anchor, bool) {
	if c, ok := m.doc.CommentAt(m.cursor); ok {
		return anchor{path: c.File, line: c.Line, side: sideOr(c.Side), hunk: c.Hunk}, true
	}

	target := m.doc.TargetAt(m.cursor)
	switch target.Kind {
	case TargetLine:
		ref, index, ok := m.doc.LineAt(m.cursor)
		if !ok {
			return anchor{}, false
		}
		return lineAnchor(ref, ref.Hunk.Lines[index]), true
	case TargetHunk:
		ref := m.doc.Hunks[target.Hunk]
		if i, ok := headlineIndex(ref.Hunk); ok {
			return lineAnchor(ref, ref.Hunk.Lines[i]), true
		}
		return anchor{path: ref.Path, side: store.SideNew, hunk: ref.ID.String()}, true
	case TargetFile:
		return anchor{path: target.Path, side: store.SideNew}, true
	default:
		return anchor{}, false
	}
}

// headlineIndex picks the line a comment on a whole hunk should attach to. An
// addition is preferred over a removal because a review note is usually about
// the code that is arriving, not the code leaving.
func headlineIndex(h git.Hunk) (int, bool) {
	fallback := -1
	for i, l := range h.Lines {
		if l.Kind == git.LineAdded {
			return i, true
		}
		if l.Kind == git.LineRemoved && fallback < 0 {
			fallback = i
		}
	}
	return fallback, fallback >= 0
}

// lineAnchor anchors to whichever side of the diff the line exists on, so a
// comment on a deletion lands on the old file.
func lineAnchor(ref HunkRef, l git.Line) anchor {
	if l.Kind == git.LineRemoved {
		return anchor{path: ref.Path, line: l.OldLine, side: store.SideOld, hunk: ref.ID.String()}
	}
	return anchor{path: ref.Path, line: l.NewLine, side: store.SideNew, hunk: ref.ID.String()}
}

func sideOr(s store.Side) store.Side {
	if s.Valid() {
		return s
	}
	return store.SideNew
}

func (m *Model) submitComment() tea.Cmd {
	body := strings.TrimSpace(m.input.Value())
	got := m.pending
	m.closeComment()
	if body == "" {
		m.status = "empty comment discarded"
		return nil
	}

	comment := store.Comment{
		File:   got.path,
		Line:   got.line,
		Side:   got.side,
		Body:   body,
		Hunk:   got.hunk,
		Author: store.AuthorUser,
	}
	// Saving reloads, and a reload normally moves the reviewer on to what is
	// left to review. Writing a note is not progress through the diff, so this
	// one puts the cursor back where it was written.
	return m.runHere("saving comment", "commented on "+got.location(), func(context.Context) error {
		_, err := m.backend.AddComment(comment)
		return err
	})
}

func (m *Model) toggleResolved() tea.Cmd {
	c, ok := m.doc.CommentAt(m.cursor)
	if !ok {
		m.status = "move to a comment to resolve it"
		return nil
	}
	want := !c.Resolved
	done := "resolved " + c.Location()
	if !want {
		done = "reopened " + c.Location()
	}
	return m.runHere("updating comment", done, func(context.Context) error {
		return m.backend.SetResolved(c.ID, want)
	})
}

func (m *Model) deleteComment() tea.Cmd {
	c, ok := m.doc.CommentAt(m.cursor)
	if !ok {
		m.status = "move to a comment to delete it"
		return nil
	}
	return m.run("deleting comment", "deleted comment on "+c.Location(), func(context.Context) error {
		return m.backend.RemoveComment(c.ID)
	})
}

// walkBusyNote is the banner shown while a provider writes a narrative. It
// doubles as the guard against a second keypress starting a second one, since
// generating is slow enough to be pressed again out of impatience.
const walkBusyNote = "generating walkthrough"

func (m *Model) walkCmd(regenerate bool) tea.Cmd {
	if m.busy == walkBusyNote {
		m.status = "already generating a walkthrough"
		return nil
	}
	m.busy = walkBusyNote
	backend, ctx := m.backend, m.ctx
	return func() tea.Msg {
		body, err := backend.Walkthrough(ctx, regenerate)
		if err != nil {
			return errMsg{err}
		}
		return walkthroughMsg{body: body}
	}
}

func (m *Model) reload(note string) tea.Cmd {
	m.busy = "reloading"
	backend, ctx := m.backend, m.ctx
	return func() tea.Msg { return load(ctx, backend, note) }
}

// toggleFollow turns the re-read timer on or off.
func (m *Model) toggleFollow() tea.Cmd {
	m.follow = !m.follow
	if !m.follow {
		m.status = "following off"
		return nil
	}
	m.status = "following the repository every " + m.pollEvery.String()
	return m.tickCmd()
}

// tickCmd schedules the next follow check.
func (m *Model) tickCmd() tea.Cmd {
	return tea.Tick(m.pollEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// pollCmd re-reads in the background without the "reloading" banner, since a
// follow check that finds nothing should be invisible.
func (m *Model) pollCmd() tea.Cmd {
	backend, ctx := m.backend, m.ctx
	return func() tea.Msg {
		msg := load(ctx, backend, "")
		loaded, ok := msg.(loadedMsg)
		if !ok {
			// A failed poll is dropped: it is usually a mid-write index lock,
			// and there is no point interrupting a review over it.
			return nil
		}
		loaded.poll = true
		return loaded
	}
}

// run performs a mutation off the UI goroutine and reloads afterwards, so the
// document never shows a state the repository has already moved past.
func (m *Model) run(busy, done string, op func(context.Context) error) tea.Cmd {
	return m.runCollapsing(busy, done, nil, op)
}

// runHere is run for the changes that are not progress through the diff — a
// note written, a comment resolved. A reload normally moves the reviewer on to
// what is left to review; these put the cursor back where it was instead.
func (m *Model) runHere(busy, done string, op func(context.Context) error) tea.Cmd {
	at := m.spot()
	cmd := m.runCollapsing(busy, done, nil, op)
	return func() tea.Msg {
		msg := cmd()
		loaded, ok := msg.(loadedMsg)
		if !ok {
			return msg
		}
		loaded.at = &at
		return loaded
	}
}

// runAdvancing is runCollapsing for a file the reviewer is done with: it folds
// the file away and leaves the cursor on the next one still to review.
//
// Like the fold, the move travels with the reload rather than happening on the
// keypress, so a stage that fails leaves the cursor on the file it failed on.
func (m *Model) runAdvancing(busy, done, path string, op func(context.Context) error) tea.Cmd {
	cmd := m.runCollapsing(busy, done, fold(path), op)
	return func() tea.Msg {
		msg := cmd()
		loaded, ok := msg.(loadedMsg)
		if !ok {
			return msg
		}
		loaded.after = path
		return loaded
	}
}

// runCollapsing is run, folding or opening files once the reload lands.
//
// The fold travels with the reload rather than being applied on the keypress so
// that a stage which fails leaves the file open — it still has to be reviewed.
func (m *Model) runCollapsing(busy, done string, collapse map[string]bool, op func(context.Context) error) tea.Cmd {
	m.busy = busy
	backend, ctx := m.backend, m.ctx
	return func() tea.Msg {
		if err := op(ctx); err != nil {
			return errMsg{err}
		}
		msg := load(ctx, backend, done)
		loaded, ok := msg.(loadedMsg)
		if !ok {
			return msg
		}
		loaded.collapse = collapse
		return loaded
	}
}

// load re-reads the session and its comments.
func load(ctx context.Context, backend Backend, note string) tea.Msg {
	session, err := backend.Reload(ctx)
	if err != nil {
		return errMsg{err}
	}
	comments, err := backend.Comments()
	if err != nil {
		return errMsg{err}
	}
	return loadedMsg{session: session, comments: comments, note: note}
}

type loadedMsg struct {
	session  *app.Session
	comments []store.Comment
	note     string
	// poll marks a load nobody asked for, which may be dropped.
	poll bool
	// collapse folds the named files away, or opens them where false, as the
	// reload is applied: staging a file gets it out of the way of what is left
	// to review, unstaging brings it back.
	collapse map[string]bool
	// at puts the cursor back on what it was on, instead of on whatever is left
	// to review in the file. Nil leaves the usual restore alone.
	at *spot
	// after names a file that has just been dealt with, moving the cursor on to
	// the next one to review instead of leaving it on what it staged.
	after string
}

type walkthroughMsg struct{ body string }

type errMsg struct{ err error }

type tickMsg struct{}

// applyLoaded swaps in a freshly read session, keeping the reviewer roughly
// where they were rather than jumping back to the top.
func (m *Model) applyLoaded(msg loadedMsg) {
	if msg.poll && !m.acceptPoll(msg) {
		return
	}
	path := m.currentPath()
	m.busy = ""
	m.err = nil
	m.session = msg.session
	m.comments = msg.comments
	// A reload that lands under an open editor takes the keyboard back, so the
	// editor has to be put away with it rather than left focused and invisible.
	m.input.Blur()
	m.mode = modeBrowse
	// The narrative is kept: it is the notes the reviewer is reading, and
	// dropping it would reorder the diff underneath them every time they staged
	// a hunk. When the code itself moved on it is marked instead, since only the
	// reviewer knows whether waiting for a new one is worth it.
	m.walkStale = m.walkStale || (m.walkLoaded && codeFingerprintOf(msg.session) != m.walkCode)

	m.setCollapsed(msg.collapse)

	m.fingerprint = fingerprintOf(msg.session)
	m.rebuild()
	switch {
	case msg.at != nil:
		m.moveToSpot(*msg.at)
	case msg.after != "":
		m.advanceFrom(msg.after)
	default:
		m.restoreCursor(path)
	}
	if msg.note != "" {
		m.status = msg.note
	}
	if msg.poll {
		m.status = "reloaded — the working tree changed"
	}
}

// acceptPoll decides whether an unrequested reload is worth applying. Redrawing
// while someone is mid-comment, or when nothing actually changed, is worse than
// waiting for the next tick.
func (m *Model) acceptPoll(msg loadedMsg) bool {
	if m.mode == modeComment {
		return false
	}
	return fingerprintOf(msg.session) != m.fingerprint
}

// fingerprintOf identifies what a session puts on screen, cheaply enough to
// compare on every poll.
//
// It walks the file entries rather than hashing Session.DiffText, which is
// git diff HEAD: that text omits untracked files and reads the same whether a
// change is staged or not, so it cannot see a new file appear or an agent stage
// something underneath the reviewer.
func fingerprintOf(s *app.Session) string {
	if s == nil {
		return ""
	}
	var b strings.Builder
	for _, f := range s.Files {
		fmt.Fprintf(&b, "%s\t%v\t%t\n", f.Path, f.State(), f.Untracked)
		writeSideFingerprint(&b, "staged", f.Staged)
		writeSideFingerprint(&b, "unstaged", f.Unstaged)
	}
	return store.Fingerprint(b.String())
}

// codeFingerprintOf identifies the changed code a narrative describes.
//
// It hashes git diff HEAD rather than the file entries: that text reads the same
// whether a change is staged or not, so a walkthrough survives the staging it is
// there to guide, and is only dated by the code actually changing.
func codeFingerprintOf(s *app.Session) string {
	if s == nil {
		return ""
	}
	return store.Fingerprint(s.DiffText)
}

func writeSideFingerprint(b *strings.Builder, side string, d *git.FileDiff) {
	if d == nil {
		return
	}
	fmt.Fprintf(b, "%s\t%v\t%t\t%s\t%s\n", side, d.Status, d.IsBinary, d.OldMode, d.NewMode)
	for _, h := range d.Hunks {
		fmt.Fprintf(b, "%s\n", h.Header())
		for _, l := range h.Lines {
			fmt.Fprintf(b, "%c%s\n", l.Kind.Origin(), l.Text)
		}
	}
}

// restoreCursor puts the cursor back on path, preferring its first unstaged
// hunk so staging repeatedly advances through what is left to review.
func (m *Model) restoreCursor(path string) {
	if path == "" {
		m.moveTo(m.doc.FirstStop())
		return
	}
	for fi, f := range m.doc.Files {
		if f.Entry.Path != path {
			continue
		}
		for _, hi := range f.Hunks {
			if !m.doc.Hunks[hi].Staged {
				m.moveTo(m.doc.RowOfHunk(hi))
				return
			}
		}
		m.moveTo(m.doc.RowOfFile(fi))
		return
	}
	m.moveTo(m.doc.Nearest(min(m.cursor, max(m.doc.Len()-1, 0))))
}

func (m *Model) currentPath() string {
	file := m.doc.FileAt(m.cursor)
	if file < 0 || file >= len(m.doc.Files) {
		return ""
	}
	return m.doc.Files[file].Entry.Path
}

func (m *Model) rebuild() {
	m.doc = Build(m.session, m.comments, m.collapsed, m.layout, WithGroups(m.groups()), WithDraft(m.draft()))
	if m.cursor >= m.doc.Len() {
		m.cursor = m.doc.LastStop()
	}
	// The file pane appears with the first file, so the diff's width is only
	// settled once the document is.
	m.renderer.SetWidth(m.diffWidth())
	m.clampTop()
	m.clampFileTop()
}

// setLayout changes how hunk bodies are laid out.
//
// Rebuilding renumbers every row, so the cursor is put back on the same line or
// hunk rather than left at the same row index — switching layout is a display
// change and should not move the reviewer.
func (m *Model) setLayout(l Layout) {
	m.layout = l
	m.relayout()
	m.status = l.String() + " layout"
}

// fileIndex looks a file up by path, since rebuilding can renumber files too.
func (m *Model) fileIndex(path string) int {
	for i, f := range m.doc.Files {
		if f.Entry.Path == path {
			return i
		}
	}
	return -1
}

// toggleCollapse folds whatever the cursor is on out of the way: a walkthrough
// step's explanation, or a file's body.
func (m *Model) toggleCollapse() {
	if step := m.doc.StepAt(m.cursor); step >= 0 {
		m.foldStep(step)
		return
	}
	file := m.doc.FileAt(m.cursor)
	if file < 0 || file >= len(m.doc.Files) {
		return
	}
	path := m.doc.Files[file].Entry.Path
	if m.collapsed[path] {
		delete(m.collapsed, path)
	} else {
		m.collapsed[path] = true
	}
	m.rebuild()
	m.moveTo(m.doc.RowOfFile(file))
}

// showFile moves to the top of a file — its walkthrough note, where it has one —
// and opens the window there, so a file jump reads the file from its first line
// instead of leaving it at the bottom of a window still filled by the file
// before it.
func (m *Model) showFile(row int) {
	if row < 0 || row >= m.doc.Len() {
		return
	}
	switch m.doc.Rows[row].Kind {
	case RowFile, RowStep:
	default:
		m.moveTo(row)
		return
	}
	m.cursor = row
	m.top = row
	m.clampTop()
	m.ensureFileVisible()
}

func (m *Model) moveTo(row int) {
	if row < 0 || row >= m.doc.Len() {
		return
	}
	m.cursor = row
	m.ensureVisible(row)
	m.ensureFileVisible()
}

// scrollDiff moves the diff window by whole lines, for the wheel. The cursor is
// dragged along as far as it must to stay on screen, so it never addresses a row
// the reviewer cannot see.
func (m *Model) scrollDiff(delta int) {
	before := m.top
	m.top += delta
	m.clampTop()
	if m.top == before {
		return
	}
	m.keepCursorInView()
	m.ensureFileVisible()
}

// keepCursorInView pulls the cursor back to the nearest row inside the window,
// so wheeling the diff moves the line cursor with it.
func (m *Model) keepCursorInView() {
	height := m.bodyHeight()
	lo, hi := m.top, m.top+height-1
	if m.cursor >= lo && m.cursor <= hi {
		return
	}
	if row := m.doc.StopBetween(lo, hi, m.cursor < lo); row >= 0 {
		m.cursor = row
	}
}

// scrollFiles moves the file pane on its own, without touching the diff.
func (m *Model) scrollFiles(delta int) {
	m.fileTop += delta
	m.clampFileTop()
}

// markedFile is the file the pane marks: the one the diff window opens on, so
// the mark names the file being read rather than a file the cursor left behind
// when the window scrolled past it. The blank row between files belongs to the
// file above, so a window starting on one is already reading the file below.
func (m *Model) markedFile() int {
	for row := m.top; row < m.top+m.bodyHeight() && row < m.doc.Len(); row++ {
		if m.doc.Rows[row].Kind != RowBlank && m.doc.Rows[row].File >= 0 {
			return m.doc.Rows[row].File
		}
	}
	return m.doc.FileAt(m.top)
}

// ensureFileVisible scrolls the pane only when the marked file has left it, so
// a pane scrolled by hand stays where it was put.
func (m *Model) ensureFileVisible() {
	height := m.bodyHeight()
	file := m.markedFile()
	if file < 0 || height <= 0 {
		return
	}
	if file < m.fileTop {
		m.fileTop = file
	}
	if file >= m.fileTop+height {
		m.fileTop = file - height + 1
	}
	m.clampFileTop()
}

func (m *Model) clampFileTop() {
	m.fileTop = min(max(m.fileTop, 0), max(0, len(m.doc.Files)-m.bodyHeight()))
}

func (m *Model) ensureVisible(row int) {
	height := m.bodyHeight()
	if row < 0 || height <= 0 {
		return
	}
	if row < m.top {
		m.top = row
	}
	if row >= m.top+height {
		m.top = row - height + 1
	}
	m.clampTop()
}

func (m *Model) clampTop() {
	m.top = min(max(m.top, 0), max(0, m.doc.Len()-m.bodyHeight()))
}

func (m *Model) resize(width, height int) {
	m.width = max(width, 20)
	m.height = max(height, 8)
	m.renderer.SetWidth(m.diffWidth())
	if m.mode == modeComment {
		m.input.SetWidth(m.draftWidth())
	}
	// A walkthrough's explanations are wrapped into rows, so a resize changes
	// how many rows the document has.
	if len(m.doc.Steps) > 0 {
		m.relayout()
	}
	m.clampTop()
	m.ensureVisible(m.cursor)
	m.ensureFileVisible()
	m.revealDraft()
}
