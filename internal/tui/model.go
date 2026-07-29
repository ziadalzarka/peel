package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/git"
	"github.com/ziadalzarka/peel/internal/store"
)

// mode is which of the UI's screens has the keyboard.
type mode int

const (
	modeBrowse mode = iota
	modeLineSelect
	modeComment
	modeWalkthrough
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

	lineHunk   int
	lineCursor int
	selected   map[int]bool
	// restoreLayout is the layout to return to after selecting lines.
	restoreLayout Layout

	mode   mode
	layout Layout

	width  int
	height int

	theme    Theme
	renderer *Renderer

	input   textarea.Model
	pending anchor

	walk       viewport.Model
	walkLoaded bool

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
		ctx:       ctx,
		backend:   backend,
		session:   session,
		comments:  comments,
		collapsed: map[string]bool{},
		selected:  map[int]bool{},
		lineHunk:  -1,
		layout:    cfg.layout,
		theme:     cfg.theme,
		renderer:  NewRenderer(cfg.theme, cfg.syntax),
		input:     newInput(),
		walk:      viewport.New(cfg.width, cfg.height),
		follow:    cfg.follow,
		pollEvery: cfg.pollEvery,
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

func newInput() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Write a review comment…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
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
		m.walkLoaded = true
		m.walk.SetContent(msg.body)
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
	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

// key routes a press to whichever screen has the keyboard.
func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	switch m.mode {
	case modeHelp:
		return m.helpKey(msg)
	case modeComment:
		return m.commentKey(msg)
	case modeWalkthrough:
		return m.walkKey(msg)
	case modeLineSelect:
		return m.lineKey(msg)
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
	case "j", "down":
		m.moveTo(m.doc.NextStop(m.cursor))
	case "k", "up":
		m.moveTo(m.doc.PrevStop(m.cursor))
	case "J", "}":
		m.moveTo(m.doc.NextFile(m.cursor))
	case "K", "{":
		m.moveTo(m.doc.PrevFile(m.cursor))
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
		return m.run("staging everything", "staged everything", m.backend.StageAll)
	case "U":
		return m.run("unstaging everything", "unstaged everything", m.backend.UnstageAll)
	case "c":
		m.openComment()
	case "x":
		return m.toggleResolved()
	case "D":
		return m.deleteComment()
	case "v":
		m.enterLineSelect()
	case `\`:
		m.setLayout(m.layout.Toggle())
	case "w":
		return m.openWalkthrough()
	case "r":
		return m.reload("reloaded")
	case "f":
		return m.toggleFollow()
	}
	return nil
}

func (m *Model) lineKey(msg tea.KeyMsg) tea.Cmd {
	changed := m.changedLines()
	if len(changed) == 0 {
		m.mode = modeBrowse
		return nil
	}
	m.status, m.err = "", nil

	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return tea.Quit
	case "esc", "q":
		m.leaveLineSelect()
	case "j", "down":
		m.lineCursor = min(m.lineCursor+1, len(changed)-1)
		m.showLine()
	case "k", "up":
		m.lineCursor = max(m.lineCursor-1, 0)
		m.showLine()
	case " ":
		index := changed[m.lineCursor]
		if m.selected[index] {
			delete(m.selected, index)
		} else {
			m.selected[index] = true
		}
	case "a":
		for _, i := range changed {
			m.selected[i] = true
		}
	case "n":
		m.selected = map[int]bool{}
	case "c":
		m.openComment()
	case "s":
		return m.stageLines()
	case "u":
		return m.unstageLines()
	case "?":
		m.mode = modeHelp
	}
	return nil
}

func (m *Model) commentKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.input.Blur()
		m.mode = modeBrowse
		m.status = "comment cancelled"
		return nil
	case "ctrl+s", "alt+enter":
		return m.submitComment()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

func (m *Model) walkKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return tea.Quit
	case "esc", "q", "w":
		m.mode = modeBrowse
		return nil
	case "r":
		return m.walkCmd(true)
	}
	var cmd tea.Cmd
	m.walk, cmd = m.walk.Update(msg)
	return cmd
}

func (m *Model) helpKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		m.quitting = true
		return tea.Quit
	}
	m.mode = modeBrowse
	return nil
}

// stageAt stages whatever the cursor addresses.
func (m *Model) stageAt() tea.Cmd {
	target := m.doc.TargetAt(m.cursor)
	switch target.Kind {
	case TargetHunk:
		ref := m.doc.Hunks[target.Hunk]
		if ref.Staged {
			m.status = "that hunk is already staged"
			return nil
		}
		sels := []git.Selection{git.WholeHunk(ref.ID)}
		return m.run("staging hunk", "staged "+ref.ID.String(), func(ctx context.Context) error {
			return m.backend.Stage(ctx, sels)
		})
	case TargetFile:
		path := target.Path
		return m.run("staging "+path, "staged "+path, func(ctx context.Context) error {
			return m.backend.StageFile(ctx, path)
		})
	default:
		m.status = "nothing to stage here"
		return nil
	}
}

// unstageAt removes whatever the cursor addresses from the index.
func (m *Model) unstageAt() tea.Cmd {
	target := m.doc.TargetAt(m.cursor)
	switch target.Kind {
	case TargetHunk:
		ref := m.doc.Hunks[target.Hunk]
		if !ref.Staged {
			m.status = "that hunk is not staged"
			return nil
		}
		sels := []git.Selection{git.WholeHunk(ref.ID)}
		return m.run("unstaging hunk", "unstaged "+ref.ID.String(), func(ctx context.Context) error {
			return m.backend.Unstage(ctx, sels)
		})
	case TargetFile:
		if !target.Staged {
			m.status = target.Path + " has nothing staged"
			return nil
		}
		path := target.Path
		return m.run("unstaging "+path, "unstaged "+path, func(ctx context.Context) error {
			return m.backend.UnstageFile(ctx, path)
		})
	default:
		m.status = "nothing to unstage here"
		return nil
	}
}

func (m *Model) stageLines() tea.Cmd {
	ref, lines, ok := m.lineSelection()
	if !ok {
		return nil
	}
	if ref.Staged {
		m.status = "those lines are already staged"
		return nil
	}
	sels := []git.Selection{{Hunk: ref.ID, Lines: lines}}
	return m.run("staging lines", fmt.Sprintf("staged %d lines", len(lines)), func(ctx context.Context) error {
		return m.backend.Stage(ctx, sels)
	})
}

func (m *Model) unstageLines() tea.Cmd {
	ref, lines, ok := m.lineSelection()
	if !ok {
		return nil
	}
	if !ref.Staged {
		m.status = "those lines are not staged"
		return nil
	}
	sels := []git.Selection{{Hunk: ref.ID, Lines: lines}}
	return m.run("unstaging lines", fmt.Sprintf("unstaged %d lines", len(lines)), func(ctx context.Context) error {
		return m.backend.Unstage(ctx, sels)
	})
}

// lineSelection returns the hunk and sorted line indexes the line-level
// operations act on.
func (m *Model) lineSelection() (HunkRef, []int, bool) {
	if m.lineHunk < 0 || m.lineHunk >= len(m.doc.Hunks) {
		m.status = "no hunk selected"
		return HunkRef{}, nil, false
	}
	lines := make([]int, 0, len(m.selected))
	for i := range m.selected {
		lines = append(lines, i)
	}
	if len(lines) == 0 {
		m.status = "select lines with space first"
		return HunkRef{}, nil, false
	}
	sort.Ints(lines)
	return m.doc.Hunks[m.lineHunk], lines, true
}

// enterLineSelect starts selecting individual lines of the hunk at the cursor.
//
// It forces the unified layout: a side-by-side row can hold a removal and an
// addition at once, so one selection mark per row could not say which of the two
// is selected.
func (m *Model) enterLineSelect() {
	target := m.doc.TargetAt(m.cursor)
	if target.Kind != TargetHunk {
		m.status = "move to a hunk to select lines"
		return
	}
	if len(m.doc.Hunks[target.Hunk].Hunk.ChangedLineIndexes()) == 0 {
		m.status = "that hunk has no changed lines"
		return
	}

	hunkID := m.doc.Hunks[target.Hunk].ID
	m.restoreLayout = m.layout
	if m.layout != LayoutUnified {
		m.layout = LayoutUnified
		m.rebuild()
	}

	m.mode = modeLineSelect
	m.lineHunk = m.findHunk(hunkID)
	m.lineCursor = 0
	m.selected = map[int]bool{}
	if row := m.doc.RowOfHunk(m.lineHunk); row >= 0 {
		m.cursor = row
	}
	m.showLine()
}

func (m *Model) leaveLineSelect() {
	hunk := m.lineHunk
	m.mode = modeBrowse
	m.selected = map[int]bool{}
	m.lineHunk = -1

	var hunkID git.HunkID
	if hunk >= 0 && hunk < len(m.doc.Hunks) {
		hunkID = m.doc.Hunks[hunk].ID
	}
	if m.restoreLayout != m.layout {
		m.layout = m.restoreLayout
		m.rebuild()
	}
	if row := m.doc.RowOfHunk(m.findHunk(hunkID)); row >= 0 {
		m.moveTo(row)
	}
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

// changedLines returns the selectable lines of the hunk being line-selected.
func (m *Model) changedLines() []int {
	if m.lineHunk < 0 || m.lineHunk >= len(m.doc.Hunks) {
		return nil
	}
	return m.doc.Hunks[m.lineHunk].Hunk.ChangedLineIndexes()
}

// focusedLine returns the hunk line index under the line-selection cursor.
func (m *Model) focusedLine() (int, bool) {
	changed := m.changedLines()
	if m.lineCursor < 0 || m.lineCursor >= len(changed) {
		return 0, false
	}
	return changed[m.lineCursor], true
}

func (m *Model) showLine() {
	index, ok := m.focusedLine()
	if !ok {
		return
	}
	m.ensureVisible(m.doc.RowOfLine(m.lineHunk, index))
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

func (m *Model) openComment() {
	got, ok := m.anchorAt()
	if !ok {
		m.status = "nothing to comment on here"
		return
	}
	m.pending = got
	m.mode = modeComment
	m.input.Reset()
	m.input.SetWidth(max(m.width-4, 20))
	m.input.SetHeight(6)
	m.input.Focus()
}

// anchorAt resolves the cursor to a comment anchor: the focused line while
// selecting lines, the first changed line of a hunk, an existing comment's own
// anchor, or the file as a whole.
func (m *Model) anchorAt() (anchor, bool) {
	if m.mode == modeLineSelect {
		if index, ok := m.focusedLine(); ok {
			ref := m.doc.Hunks[m.lineHunk]
			return lineAnchor(ref, ref.Hunk.Lines[index]), true
		}
	}
	if c, ok := m.doc.CommentAt(m.cursor); ok {
		return anchor{path: c.File, line: c.Line, side: sideOr(c.Side), hunk: c.Hunk}, true
	}

	target := m.doc.TargetAt(m.cursor)
	switch target.Kind {
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
	m.input.Blur()
	m.mode = modeBrowse
	if body == "" {
		m.status = "empty comment discarded"
		return nil
	}

	got := m.pending
	comment := store.Comment{
		File:   got.path,
		Line:   got.line,
		Side:   got.side,
		Body:   body,
		Hunk:   got.hunk,
		Author: store.AuthorUser,
	}
	return m.run("saving comment", "commented on "+got.location(), func(context.Context) error {
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
	return m.run("updating comment", done, func(context.Context) error {
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

func (m *Model) openWalkthrough() tea.Cmd {
	m.mode = modeWalkthrough
	if m.walkLoaded {
		return nil
	}
	return m.walkCmd(false)
}

func (m *Model) walkCmd(regenerate bool) tea.Cmd {
	m.busy = "generating walkthrough"
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
	m.busy = busy
	backend, ctx := m.backend, m.ctx
	return func() tea.Msg {
		if err := op(ctx); err != nil {
			return errMsg{err}
		}
		return load(ctx, backend, done)
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
	m.mode = modeBrowse
	m.lineHunk = -1
	m.selected = map[int]bool{}
	// The diff changed, so the cached narrative describes the old one.
	m.walkLoaded = false

	m.fingerprint = fingerprintOf(msg.session)
	m.rebuild()
	m.restoreCursor(path)
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
	if m.mode == modeComment || m.mode == modeLineSelect {
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
	m.doc = Build(m.session, m.comments, m.collapsed, m.layout)
	if m.cursor >= m.doc.Len() {
		m.cursor = m.doc.LastStop()
	}
	m.clampTop()
}

// setLayout changes how hunk bodies are laid out.
//
// Rebuilding renumbers every row, so the cursor is put back on the same hunk
// rather than left at the same row index — switching layout is a display change
// and should not move the reviewer.
func (m *Model) setLayout(l Layout) {
	target := m.doc.TargetAt(m.cursor)
	var onHunk git.HunkID
	if target.Kind == TargetHunk {
		onHunk = m.doc.Hunks[target.Hunk].ID
	}
	path := m.currentPath()

	m.layout = l
	m.rebuild()

	if row := m.doc.RowOfHunk(m.findHunk(onHunk)); row >= 0 {
		m.moveTo(row)
	} else if row := m.doc.RowOfFile(m.fileIndex(path)); row >= 0 {
		m.moveTo(row)
	}
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

func (m *Model) toggleCollapse() {
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

func (m *Model) moveTo(row int) {
	if row < 0 || row >= m.doc.Len() {
		return
	}
	m.cursor = row
	m.ensureVisible(row)
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
	m.walk.Width = max(m.width-4, 20)
	m.walk.Height = max(m.bodyHeight(), 3)
	if m.mode == modeComment {
		m.input.SetWidth(max(m.width-4, 20))
	}
	m.clampTop()
	m.ensureVisible(m.cursor)
}
