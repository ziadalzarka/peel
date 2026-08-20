package tui

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
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
	modeConfirm
	modeFind
	// modeReview is the summary being written for a review about to be posted,
	// and modeReviewEvent is the choice of what posting it does to the pull
	// request. The question that actually sends it is a modeConfirm after those.
	modeReview
	modeReviewEvent
)

// Model is the review UI's state.
//
// Everything it needs from the outside arrives through Backend, so Update can be
// driven key by key in a test with no terminal and no repository.
type Model struct {
	ctx     context.Context
	backend Backend

	session  *app.Session
	comments []store.Comment
	// agentCommentsOff keeps the agent's notes out of the diff, leaving the
	// reviewer's own. It is a display change and nothing else: what is hidden is
	// still in the store, and `A` puts it back.
	agentCommentsOff bool
	doc              Document
	collapsed        map[string]bool
	// stagedFolds are the files folded away because they were staged, as opposed
	// to put away by hand. A change arriving in one of those is unreviewed work
	// in a file the reviewer was finished with, so it opens again; a file folded
	// deliberately was closed on purpose and stays closed.
	stagedFolds map[string]bool
	// sideFolds overrides, by path, whether a part-staged file's index half is
	// hidden. It holds only what the reviewer has said with `space`; everything
	// else takes the default.
	sideFolds map[string]bool
	// context is each side's own copy of the file its hunks are numbered
	// against, which is what the unchanged code around a hunk is read out of. It
	// is dropped whenever the diff moves on and read again behind the screen, so
	// nothing is ever revealed out of a file that has since changed.
	context map[FileSide][]string
	// revealed is how far each run of hidden code has been opened, by `space` on
	// the row standing where it was left out.
	revealed map[ExpandKey]int

	cursor int
	// sel is the run of lines marked with shift and an arrow to write one note
	// about, and nil when nothing is marked.
	sel *selection
	top int
	// fileRows is the side pane's tree: the changed files under the directories
	// they live in, one row each. It is rebuilt with the document, since it is
	// the same files laid out a second way.
	fileRows []paneRow
	// fileTop is the first row drawn in the side pane. It scrolls
	// independently of the diff, and only follows the diff when the marked file
	// has left the pane.
	fileTop     int
	filePaneOff bool
	// xoff is how far the code has been slid sideways under a pinned gutter, for
	// reading a line too long for the pane. It is a property of the window
	// rather than of the cursor, so moving between files keeps it.
	xoff int

	mode   mode
	layout Layout

	width  int
	height int

	theme    Theme
	renderer *Renderer

	input   textarea.Model
	pending anchor
	// editing is the ID of the note the editor was opened on, and empty when
	// what is being written is a new one. It is what makes `enter` rewrite that
	// note rather than leave a second one beside it.
	editing string
	// ask is the question the footer is putting to the reviewer, and what to do
	// if they say yes. It is set only in modeConfirm.
	ask *confirm
	// posting is the review being sent to the code host: the summary written for
	// it and what it does to the pull request. It is held from `P` until the
	// question is answered, since the editor it was written in is gone by then.
	posting *posting
	// find is the file search, and is only being typed into in modeFind.
	find finder
	// helpTop is the first line of the help screen drawn, for the terminals too
	// short to hold every key at once.
	helpTop int

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

	// moves is where the cursor goes once a file has been dealt with, one
	// setting for `s` and one for `space`. It is read from git config at
	// startup, since the keys it governs draw before they ask git anything.
	moves app.Moves

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

	// writes counts the changes on screen whose write has not been read back
	// yet. The goroutines doing the writing count themselves out of it, so it is
	// atomic; what it is for is in optimistic.go.
	writes atomic.Int32
	// writing gates the next write on the one before it, keeping peel's git
	// calls in the order the keys were pressed.
	writing chan struct{}
	// unsavedIDs numbers the comments drawn before their write landed.
	unsavedIDs int

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
	moves     app.Moves
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

// WithMoves sets where the cursor goes after a file has been staged and after
// one has been folded away.
func WithMoves(m app.Moves) Option { return func(o *options) { o.moves = m } }

// New builds the review UI for a session and the comments already on it.
func New(ctx context.Context, backend Backend, session *app.Session, comments []store.Comment, opts ...Option) *Model {
	cfg := options{theme: DefaultTheme(), syntax: NewHighlighter(), width: 100, height: 30}
	for _, opt := range opts {
		opt(&cfg)
	}

	m := &Model{
		ctx:         ctx,
		backend:     backend,
		session:     session,
		comments:    comments,
		collapsed:   map[string]bool{},
		stagedFolds: map[string]bool{},
		sideFolds:   map[string]bool{},
		revealed:    map[ExpandKey]int{},
		layout:      cfg.layout,
		theme:       cfg.theme,
		renderer:    NewRenderer(cfg.theme, cfg.syntax),
		input:       newInput(cfg.theme),
		walkFolded:  map[int]bool{},
		moves:       cfg.moves.OrDefault(),
		follow:      cfg.follow,
		pollEvery:   cfg.pollEvery,
	}
	if m.pollEvery <= 0 {
		m.pollEvery = defaultPollEvery
	}
	m.fingerprint = fingerprintOf(session)
	m.restoreFolds()
	m.restoreAgentComments()
	m.resize(cfg.width, cfg.height)
	m.rebuild()
	m.cursor = m.doc.FirstStop()
	return m
}

// restoreFolds opens the review with the files folded away that were folded
// when it was last read, so a pass carried over from yesterday still shows what
// is left rather than starting again from the top.
//
// Why each one was folded is not written down, but it does not have to be: a
// folded file with all its changes in the index was folded by staging, since
// that is the only way a file ends up in both of those states. So a change
// landing in it tomorrow still opens it, the way it would have today.
func (m *Model) restoreFolds() {
	folded, err := m.backend.Folded()
	if err != nil {
		m.err = err
		return
	}
	for _, path := range folded {
		entry, ok := m.session.Entry(path)
		if !ok {
			continue
		}
		m.collapsed[path] = true
		if entry.State() == git.StateStaged {
			m.stagedFolds[path] = true
		}
	}
}

// restoreAgentComments opens the review with the agent's notes hidden if that
// is how it was last left, so a diff read without them stays that way rather
// than putting a review back that has been dealt with already.
//
// A review with no agent notes in it opens showing them whatever was written
// down. There is nothing to take out, and `A` says so rather than lifting a
// filter, so honouring one here would leave a header claiming notes are hidden
// and no key to disagree with it.
func (m *Model) restoreAgentComments() {
	hidden, err := m.backend.AgentCommentsHidden()
	if err != nil {
		m.err = err
		return
	}
	m.agentCommentsOff = hidden && len(agentComments(m.comments)) > 0
}

// draftMinHeight and draftMaxHeight bound the inline editor. It opens small, so
// it barely parts the diff, and grows a row per line written until it would take
// the pane over — past that it scrolls inside itself.
const (
	draftMinHeight = 3
	draftMaxHeight = 12
)

// commentPlaceholder is what the editor says before a note is written. The
// summary editor `P` opens borrows the same textarea and says something else
// while it holds it, so this is what it is put back to.
const commentPlaceholder = "Write a review comment…"

func newInput(theme Theme) textarea.Model {
	ta := textarea.New()
	ta.Placeholder = commentPlaceholder
	ta.ShowLineNumbers = false
	ta.CharLimit = 4000
	// The editor draws in the comment's own bar and colour, so a note being
	// written reads as the note it is about to become. The line being typed on
	// takes the same colour rather than the library's highlight band, which would
	// sit as a block of background across a diff that now tints its own lines.
	ta.Prompt = "┃ "
	// Enter saves, since most comments are one line and reaching for a chord to
	// finish one is the wrong default. Writing a second line is the deliberate
	// press: shift+enter, which reaches the editor as alt+enter, and alt+enter
	// itself for the terminals that keep shift+enter to themselves.
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter"),
		key.WithHelp("shift+enter", "new line"),
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

// Init satisfies tea.Model.
//
// The session and its comments are already loaded. What is left is the follow
// timer and the copies of the files the diff is measured against — read behind
// the first frame, since nothing on screen waits on them: until they land the
// diff is what git printed, with no offer to read past it.
func (m *Model) Init() tea.Cmd {
	if m.follow {
		return tea.Batch(m.contextCmd(), m.tickCmd())
	}
	return m.contextCmd()
}

// Update satisfies tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case loadedMsg:
		return m, m.applyLoaded(msg)
	case contextMsg:
		m.setContext(msg)
		return m, nil
	case walkthroughMsg:
		m.busy = ""
		m.showWalkthrough(msg.body)
		return m, nil
	case postedMsg:
		m.busy = ""
		m.status = msg.note
		return m, nil
	case errMsg:
		m.busy = ""
		m.err = msg.err
		return m, nil
	case revertMsg:
		m.restore(msg.before)
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
	seq, ok := rawBytes(msg)
	if !ok {
		return m, nil
	}
	if key, named := unnamedKey(seq); named {
		return m, m.key(key)
	}
	if cmdLetter(seq) == 'p' {
		return m, m.cmdFind()
	}
	return m, nil
}

// rawBytes is what a press bubbletea could not name carries: the bytes the
// terminal sent for it.
//
// These arrive as bubbletea's own unexported message, so there is no type to
// compare against — only the shape of what it holds, which is what this reads.
func rawBytes(msg tea.Msg) (string, bool) {
	v := reflect.ValueOf(msg)
	if v.Kind() != reflect.Slice || v.Type().Elem().Kind() != reflect.Uint8 {
		return "", false
	}
	return string(v.Bytes()), true
}

// cmdFind opens the file search, which is what cmd+p does and nothing else
// does. There is no key of bubbletea's to turn the press into — no other key
// peel binds means go to a file — so it is routed by hand from the bytes, and
// says nothing while a comment, a question or the search itself is on screen.
func (m *Model) cmdFind() tea.Cmd {
	if m.mode == modeBrowse {
		m.openFind()
	}
	return nil
}

// shiftEnterSeqs are what a terminal sends for shift+enter when it can say
// which modifier was held: the CSI u form kitty and the terminals that followed
// it use, and the older modifyOtherKeys form — which is what Ghostty sends by
// default.
//
// bubbletea has no key of its own for either. It reads the sequence, finds
// nothing to call it, and hands the raw bytes on, so shift+enter arrives as a
// press with no name and is dropped.
var shiftEnterSeqs = []string{"\x1b[13;2u", "\x1b[27;2;13~"}

// modifiedArrowRe matches an arrow press a terminal has named a modifier for:
// `ESC [ 1 ; <modifiers> <A|B>`, with the event type kitty appends when it is
// asked to report one. The modifier is a bitmask offset by one — shift 1, alt 2,
// ctrl 4 — so `1;9A` is cmd+↑ and `1;10A` is cmd+shift+↑.
//
// bubbletea names the combinations of shift, alt and ctrl itself; what is left
// for this to read is the ones with cmd in them.
var modifiedArrowRe = regexp.MustCompile(`^\x1b\[1;(\d+)(?::\d+)?([AB])$`)

// cmdKeyRe matches a letter press a terminal has named a modifier for, in the
// two forms terminals write one: kitty's `ESC [ <letter> ; <modifiers> u`, with
// the event type it appends when it is asked to report one, and the older
// modifyOtherKeys `ESC [ 27 ; <modifiers> ; <letter> ~` — the same two forms
// shiftEnterSeqs is written out for. The letter is its code point, so `112` is
// `p`.
//
// bubbletea names no press in either form, so what reaches peel is the bytes.
var cmdKeyRe = regexp.MustCompile(`^\x1b\[(?:(\d+);(\d+)(?::\d+)?u|27;(\d+);(\d+)~)$`)

// cmdBits are the places cmd can take in that mask. Terminals disagree on which
// it is — kitty's protocol calls the key super and gives it bit 8, xterm's older
// encoding has no super and gives bit 8 to meta, which is what a terminal
// configured to send cmd as meta then uses — so both are read as cmd. Neither is
// a key peel binds otherwise.
//
// Which other modifiers are held alongside does not matter: cmd and an arrow is
// the press being read, and a terminal reporting cmd+shift+↓ means the same
// thing by it.
const cmdBits = 8 | 32

// unnamedKey turns a press bubbletea could not name into one peel already
// handles, and reports whether it was one.
//
//   - shift+enter becomes the alt+enter the editor writes a new line on. A
//     terminal that says nothing about the shift key sends a bare carriage
//     return instead, and no program can tell that press from enter.
//   - cmd+↑ and cmd+↓ become home and end, the first and last row. Whether they
//     arrive at all is the terminal's to decide: several keep cmd to themselves
//     for scrollback, and there is nothing an application can do about that.
func unnamedKey(seq string) (tea.KeyMsg, bool) {
	if slices.Contains(shiftEnterSeqs, seq) {
		return tea.KeyMsg{Type: tea.KeyEnter, Alt: true}, true
	}
	if match := modifiedArrowRe.FindStringSubmatch(seq); match != nil {
		mods, err := strconv.Atoi(match[1])
		if err != nil || (mods-1)&cmdBits == 0 {
			return tea.KeyMsg{}, false
		}
		if match[2] == "A" {
			return tea.KeyMsg{Type: tea.KeyHome}, true
		}
		return tea.KeyMsg{Type: tea.KeyEnd}, true
	}
	return tea.KeyMsg{}, false
}

// cmdLetter is the letter a terminal has reported cmd being held on, and zero
// for a sequence that is not one — including a letter reported for some other
// modifier, which is a press meant for something else.
func cmdLetter(seq string) rune {
	match := cmdKeyRe.FindStringSubmatch(seq)
	if match == nil {
		return 0
	}
	// The two forms write the letter and the modifiers in opposite orders.
	code, mods := match[1], match[2]
	if code == "" {
		code, mods = match[4], match[3]
	}
	held, err := strconv.Atoi(mods)
	if err != nil || (held-1)&cmdBits == 0 {
		return 0
	}
	letter, err := strconv.Atoi(code)
	if err != nil {
		return 0
	}
	return rune(letter)
}

// wheelLines is how far one notch of the wheel scrolls.
const wheelLines = 3

// wheelColumns is how far one notch of a horizontal wheel slides the code.
// Shorter than a key press, since a terminal reports a trackpad swipe as a
// stream of notches rather than as one.
const wheelColumns = 4

// mouse routes a wheel notch to whatever it should move.
func (m *Model) mouse(msg tea.MouseMsg) tea.Cmd {
	if m.mode != modeBrowse {
		return nil
	}
	// Reaching for the mouse is doing something else, the way any other key is:
	// a wheel notch drags the cursor off the run it was marking.
	m.sel = nil

	switch msg.Button {
	case tea.MouseButtonWheelDown:
		m.wheel(msg, wheelLines)
	case tea.MouseButtonWheelUp:
		m.wheel(msg, -wheelLines)
	case tea.MouseButtonWheelLeft:
		m.scrollCode(-wheelColumns)
	case tea.MouseButtonWheelRight:
		m.scrollCode(wheelColumns)
	}
	return nil
}

// wheel sends a vertical notch to whichever pane the pointer is over, so the
// file list and the diff scroll separately.
//
// A horizontal notch skips this and always reaches the diff: the file pane
// shortens paths from the left already and has nothing to slide.
func (m *Model) wheel(msg tea.MouseMsg, delta int) {
	if pane := m.filePaneWidth(); pane > 0 && msg.X < pane {
		m.scrollFiles(delta)
		return
	}
	m.scrollDiff(delta)
}

// key routes a press to whichever screen has the keyboard.
func (m *Model) key(msg tea.KeyMsg) tea.Cmd {
	switch m.mode {
	case modeHelp:
		return m.helpKey(msg)
	case modeComment:
		return m.commentKey(msg)
	case modeConfirm:
		return m.confirmKey(msg)
	case modeFind:
		return m.findKey(msg)
	case modeReview:
		return m.reviewKey(msg)
	case modeReviewEvent:
		return m.reviewEventKey(msg)
	default:
		return m.browseKey(msg)
	}
}

// leapLines is how far the brackets move the cursor when nothing on the way
// stops them sooner: far enough to cross a hunk in one press, short enough to
// keep the row it lands on in sight of the one it left.
const leapLines = 10

func (m *Model) browseKey(msg tea.KeyMsg) tea.Cmd {
	m.status, m.err = "", nil

	key := msg.String()
	// Marked lines are held, not entered: anything that is not extending the run
	// or writing the note it was marked for lets it go.
	if !keepsSelection(key) {
		m.sel = nil
	}

	switch key {
	case "q", "ctrl+c":
		return m.quit()
	case "?":
		m.mode, m.helpTop = modeHelp, 0
	case "j":
		m.moveTo(m.doc.NextMark(m.cursor))
	case "k":
		m.moveTo(m.doc.PrevMark(m.cursor))
	case "down":
		m.moveTo(m.doc.NextStop(m.cursor))
	case "up":
		m.moveTo(m.doc.PrevStop(m.cursor))
	case "]":
		m.moveTo(m.doc.Leap(m.cursor, leapLines))
	case "[":
		m.moveTo(m.doc.Leap(m.cursor, -leapLines))
	case "shift+down":
		m.mark(1)
	case "shift+up":
		m.mark(-1)
	case "alt+down":
		m.showFile(m.doc.NextFile(m.cursor))
	case "alt+up":
		m.showFile(m.doc.PrevFile(m.cursor))
	case "}":
		m.scrollFiles(1)
	case "{":
		m.scrollFiles(-1)
	case "l", "right":
		m.scrollCode(codeStep)
	case "h", "left":
		m.scrollCode(-codeStep)
	case "0":
		m.scrollCode(-m.xoff)
	case "$":
		m.scrollCode(m.maxCodeOffset())
	case "b":
		m.toggleFilePane()
	case "g", "home":
		m.moveTo(m.doc.FirstStop())
	case "G", "end":
		m.moveTo(m.doc.LastStop())
	case "ctrl+d", "pgdown":
		m.moveTo(m.doc.Nearest(m.cursor + m.bodyHeight()/2))
	case "ctrl+u", "pgup":
		m.moveTo(m.doc.Nearest(m.cursor - m.bodyHeight()/2))
	case " ":
		m.toggleCollapse()
	case "s":
		return m.stageAt()
	case "u":
		return m.unstageAt()
	case "o":
		return m.openAt()
	case "a":
		return m.stageEverything()
	case "U":
		return m.unstageEverything()
	case "c":
		m.openComment()
	case "e":
		m.editComment()
	case "x":
		return m.toggleResolved()
	case "D":
		return m.deleteComment()
	case "C":
		return m.copyComments()
	case "A":
		m.toggleAgentComments()
	case "X":
		m.askClearAgentComments()
	case "P":
		m.openReview()
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

// selection is a run of lines marked to write one note about: a hunk, and the
// two ends of the run inside it.
//
// The ends are positions in the hunk's body rather than row numbers, so the run
// survives the document being laid out again — which is what opening the comment
// editor inside it does. anchor is the line shift was first held on and stays
// put; head is the cursor, so reversing the arrow gives lines back instead of
// starting a second run.
type selection struct {
	hunk         git.HunkID
	anchor, head int
}

func (s selection) lo() int { return min(s.anchor, s.head) }
func (s selection) hi() int { return max(s.anchor, s.head) }

// keepsSelection reports the keys a marked run survives: the ones that extend
// it, and the ones that write the note it was marked for.
//
// Everything else is the reviewer doing something else, and a run still marked
// behind them is a note about to land on lines they had stopped looking at. So
// the run is not something to dismiss — it is let go of by carrying on.
func keepsSelection(key string) bool {
	switch key {
	case "shift+down", "shift+up", "c", "C":
		return true
	}
	return false
}

// mark takes one more line of the diff into the run, or gives one back when the
// arrow reverses.
//
// The run starts on the line under the cursor and grows with the cursor's own
// movement, so what is marked is what has been moved over. It stops at the ends
// of the hunk: a note's two numbers are only a range if the file holds every
// line between them, and between one hunk and the next they number code that is
// not on screen.
func (m *Model) mark(delta int) {
	hunk, at, ok := m.lineRowAt(m.cursor)
	if !ok {
		m.status = "move to a line of the diff to mark lines to comment on"
		return
	}
	if m.sel == nil || m.sel.hunk != m.doc.Hunks[hunk].ID {
		m.sel = &selection{hunk: m.doc.Hunks[hunk].ID, anchor: at, head: at}
	}

	rows := m.doc.LineRows(hunk)
	next := m.sel.head + delta
	if next < 0 || next >= len(rows) {
		m.status = "the run stops at the end of the hunk"
		return
	}
	m.sel.head = next
	m.moveTo(rows[next])
	m.status = plural(m.sel.hi()-m.sel.lo()+1, "line") + " marked — c writes one note on them"
}

// lineRowAt resolves a row to the hunk it is a body line of, and how far down
// that body it sits.
func (m *Model) lineRowAt(row int) (hunk, at int, ok bool) {
	if row < 0 || row >= m.doc.Len() {
		return 0, 0, false
	}
	r := m.doc.Rows[row]
	if r.Kind != RowLine {
		return 0, 0, false
	}
	for i, body := range m.doc.LineRows(r.Hunk) {
		if body == row {
			return r.Hunk, i, true
		}
	}
	return 0, 0, false
}

// selectedRows is the span of rows the marked run covers, and false when
// nothing is marked or the hunk it was marked in has left the document.
func (m *Model) selectedRows() (lo, hi int, ok bool) {
	if m.sel == nil {
		return 0, 0, false
	}
	rows := m.doc.LineRows(m.findHunk(m.sel.hunk))
	if m.sel.hi() >= len(rows) {
		return 0, 0, false
	}
	return rows[m.sel.lo()], rows[m.sel.hi()], true
}

// confirm is a question the footer is putting to the reviewer, and what to do
// if they say yes.
type confirm struct {
	question string
	yes      func() tea.Cmd
}

// confirmKey answers the question in the footer. Only `y` is a yes, so a key
// pressed out of habit while it is up cancels rather than deletes.
func (m *Model) confirmKey(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		return m.quit()
	}
	ask := m.ask
	m.mode, m.ask = modeBrowse, nil
	if ask == nil || msg.String() != "y" {
		m.status = "cancelled"
		return nil
	}
	return ask.yes()
}

func (m *Model) commentKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		editing := m.editing != ""
		m.closeComment()
		m.status = "comment cancelled"
		if editing {
			m.status = "edit cancelled — the comment is as it was"
		}
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
	want := m.draftHeight()
	if want == m.input.Height() {
		return
	}
	m.input.SetHeight(want)
	m.relayout()
	m.revealDraft()
}

// draftHeight is the room what is written needs, within what the editor is
// allowed to take from the diff.
func (m *Model) draftHeight() int {
	return min(max(m.input.LineCount(), draftMinHeight), draftMaxHeight)
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
	// under and over are the notes drawn either side of comment, for the notes
	// that hang off no line and are stacked under their file. They are what the
	// cursor falls back to when comment has been deleted, since the row above
	// that stack is the end of a diff the note was never about.
	under, over string
	hunk        git.HunkID
	// line indexes into the hunk's lines, and is -1 when the cursor is not on
	// one. Hunk IDs carry the hunk header, so a hunk that kept its ID kept its
	// lines too and the index still names the same code.
	line int
	// side names the half of the file the cursor is heading, when it is on one
	// of the two headings rather than in the diff under them.
	side store.Origin
}

// spot records where the cursor is, so a rebuild can put it back.
//
// A comment records the line it hangs off as well as its own ID, since the
// rebuild that follows deleting or hiding one is the rebuild where the ID names
// nothing: the cursor belongs on the code the comment was about, not at the top
// of the file. A comment with no line to hang off records the notes stacked
// beside it instead, which is the same rule where there is no code to fall back
// to.
func (m *Model) spot() spot {
	at := spot{path: m.currentPath(), line: -1}
	row := m.cursor
	if c, ok := m.doc.CommentAt(row); ok {
		at.comment = c.ID
		// A note on the file as a whole, and one whose code has been rewritten
		// out from under it, are both drawn under their file rather than on a
		// line — stacked one on the next, with the end of the diff above them.
		// Losing one of those puts the cursor on its neighbour in the stack, not
		// on code the note was never about.
		if m.doc.Rows[row].Hunk < 0 {
			at.under, at.over = m.doc.StackedAround(row)
		}
		row = m.doc.AnchorOf(row)
	}
	if side := m.doc.SideAt(row); side >= 0 {
		at.side = m.doc.Sides[side].Origin()
		return at
	}
	switch target := m.doc.TargetAt(row); target.Kind {
	case TargetHunk:
		at.hunk = m.doc.Hunks[target.Hunk].ID
	case TargetLine:
		if ref, index, ok := m.doc.LineAt(row); ok {
			at.hunk, at.line = ref.ID, index
		}
	}
	return at
}

// moveToSpot puts the cursor back on what spot named, falling back through the
// line, the hunk and then the file when the comment or the exact line has gone.
func (m *Model) moveToSpot(at spot) {
	for _, id := range []string{at.comment, at.under, at.over} {
		if row := m.doc.RowOfComment(id); row >= 0 {
			m.moveTo(row)
			return
		}
	}
	if at.side != "" {
		if row := m.doc.RowOfSide(at.path, at.side); row >= 0 {
			m.moveTo(row)
			return
		}
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

// helpKey closes the help screen, or reads further down it — the list of keys
// is longer than a short terminal, and the same keys that move the diff move it.
func (m *Model) helpKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "down", "j":
		m.scrollHelp(1)
	case "up", "k":
		m.scrollHelp(-1)
	case " ", "ctrl+d", "pgdown":
		m.scrollHelp(m.bodyHeight() / 2)
	case "ctrl+u", "pgup":
		m.scrollHelp(-m.bodyHeight() / 2)
	default:
		m.mode = modeBrowse
	}
	return nil
}

func (m *Model) scrollHelp(delta int) {
	m.helpTop = min(max(m.helpTop+delta, 0), m.helpBottom(m.helpLines()))
}

// quit leaves, once the changes already pressed have been written.
//
// A change is on screen the moment it is pressed, which makes quitting straight
// after one the natural thing to do — so the last writes are waited for rather
// than killed with the process. Waiting on the tail of the queue waits for all
// of them, since each write opens the gate for the next only once it is done.
func (m *Model) quit() tea.Cmd {
	m.quitting = true
	writing := m.writing
	if writing == nil || m.writes.Load() == 0 {
		return tea.Quit
	}
	return func() tea.Msg {
		<-writing
		return tea.Quit()
	}
}

// stageAt stages the file the cursor is in.
//
// A file is the smallest thing that can be staged, so a hunk header or a diff
// line stages the file it belongs to. The file folds away once it is staged and
// the cursor carries on to the next file with work still out of the index,
// since this one has been dealt with — `space` opens it again. Where the cursor
// goes is `peel.afterStage`, for the passes that read the diff in another
// order.
func (m *Model) stageAt() tea.Cmd {
	file, ok := m.doc.FileTargetAt(m.cursor)
	if !ok {
		m.status = "nothing to stage here"
		return nil
	}
	path := file.Entry.Path
	if file.Orphan {
		m.status = path + " has no changes to stage — its notes outlived them"
		return nil
	}
	if file.Entry.State() == git.StateStaged {
		// Nothing to write, but `s` still means "done with this one" — a file
		// reopened with `space` folds again, and moves on, without a pointless git
		// call.
		m.status = path + " is already staged"
		m.foldStaged(path)
		return nil
	}
	if !m.canStage() {
		return nil
	}
	return m.apply(func() {
		m.session = restaged(m.session, true, only(path))
		m.status = "staged " + path
		m.foldStaged(path)
	}, func(ctx context.Context) error {
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
	if !m.canStage() {
		return nil
	}
	return m.apply(func() {
		m.session = restaged(m.session, false, only(path))
		m.setCollapsed(unfold(path))
		m.status = "unstaged " + path
		m.relayout()
	}, func(ctx context.Context) error {
		return m.backend.UnstageFile(ctx, path)
	})
}

// stageEverything and unstageEverything are `a` and `U`: the whole tree in one
// press, leaving the screen a pass of single files would have left.
func (m *Model) stageEverything() tea.Cmd {
	if !m.canStage() {
		return nil
	}
	return m.apply(func() {
		m.session = restaged(m.session, true, every)
		folds := m.foldEvery(true)
		m.setCollapsed(folds)
		for path := range folds {
			m.stagedFolds[path] = true
		}
		m.status = "staged everything"
		m.relayout()
	}, m.backend.StageAll)
}

func (m *Model) unstageEverything() tea.Cmd {
	if !m.canStage() {
		return nil
	}
	return m.apply(func() {
		m.session = restaged(m.session, false, every)
		m.setCollapsed(m.foldEvery(false))
		m.status = "unstaged everything"
		m.relayout()
	}, m.backend.UnstageAll)
}

// canStage reports whether staging applies here, saying why on the spot when it
// does not. A read-only session is refused on the keypress rather than shown a
// change that would have to be taken back.
func (m *Model) canStage() bool {
	if err := m.session.NotStageable(); err != nil {
		m.err = err
		return false
	}
	return true
}

// openAt hands the file the cursor is in to the desktop, for the times the diff
// is not enough and the whole file has to be read — or edited — outside peel.
//
// Nothing about the diff changes, so there is nothing to reload and nothing to
// queue: the open is reported as done straight away, and only comes back if the
// desktop had nothing to open it with.
func (m *Model) openAt() tea.Cmd {
	file, ok := m.doc.FileTargetAt(m.cursor)
	if !ok {
		m.status = "nothing to open here"
		return nil
	}
	path := file.Entry.Path
	before := m.snapshot()
	m.status = "opened " + path
	backend, ctx := m.backend, m.ctx
	return func() tea.Msg {
		if err := backend.OpenFile(ctx, path); err != nil {
			return revertMsg{err: err, before: before}
		}
		return nil
	}
}

// fold and unfold name the two one-file collapse changes staging makes.
func fold(path string) map[string]bool   { return map[string]bool{path: true} }
func unfold(path string) map[string]bool { return map[string]bool{path: false} }

// setCollapsed folds the named files away, or opens the ones marked false. It is
// the only place folds change, so it is also where they are written down.
func (m *Model) setCollapsed(collapse map[string]bool) {
	changed := false
	for path, hide := range collapse {
		if m.collapsed[path] == hide {
			continue
		}
		if hide {
			m.collapsed[path] = true
		} else {
			delete(m.collapsed, path)
			// An open file is not folded for any reason, staging included.
			delete(m.stagedFolds, path)
		}
		changed = true
	}
	// Every reload comes through here, most of them with nothing to change —
	// follow mode would otherwise write the same folds every few seconds.
	if changed {
		m.saveFolds()
	}
}

// saveFolds records what is folded away for the next time this review is opened.
//
// A fold that fails to persist is worth saying but not worth undoing: the file
// is still folded on screen, and the reviewer's place in the pass is intact for
// as long as peel is running.
func (m *Model) saveFolds() {
	paths := make([]string, 0, len(m.collapsed))
	for path := range m.collapsed {
		// Only what is in the diff now: a file whose change has been committed
		// away is done with, and the next change to it is a new thing to read,
		// not something to hide.
		if _, ok := m.session.Entry(path); ok {
			paths = append(paths, path)
		}
	}
	if err := m.backend.SetFolded(paths); err != nil {
		m.err = err
	}
}

// foldNow folds a file away immediately, without waiting on git, and carries
// the pass on under the rule the key that folded it goes by.
func (m *Model) foldNow(path string, move app.Move) {
	m.setCollapsed(fold(path))
	m.rebuild()
	m.advanceFrom(path, move)
}

// foldStaged folds a file away because it has just been staged, and records
// that that is why.
//
// The distinction is what a change arriving in the file later turns on: staging
// says "this file is dealt with" about the changes that went into the index, and
// nothing about the ones that turn up afterwards — so those open the file again.
// A file put away by hand was put away deliberately and stays that way.
func (m *Model) foldStaged(path string) {
	m.stagedFolds[path] = true
	m.foldNow(path, m.moves.AfterStage)
}

// reopenStagedChanged opens the files that were folded away by staging and have
// since picked up working-tree changes, and returns them.
//
// What arrived is unreviewed, and it arrived in a file whose header says `✓` —
// the one place a change can hide from a pass through the diff. Its index half
// stays folded, so what the file opens on is exactly what is new.
func (m *Model) reopenStagedChanged() []string {
	var opened []string
	for path := range m.stagedFolds {
		entry, ok := m.session.Entry(path)
		if !ok {
			// The change was committed away. What is left is not a fold of this
			// review's, and the next change to that file is a new thing to read.
			delete(m.stagedFolds, path)
			continue
		}
		if entry.Unstaged == nil || !m.collapsed[path] {
			continue
		}
		opened = append(opened, path)
	}
	sort.Strings(opened)
	for _, path := range opened {
		m.setCollapsed(unfold(path))
	}
	return opened
}

// advanceFrom moves the cursor on to the next file the pass carries on with
// once path has been dealt with. It stays on path when there is no such file,
// since a file that has just been folded is a better place to be than an
// arbitrary one — and when the setting says the cursor was never to move.
func (m *Model) advanceFrom(path string, move app.Move) {
	if move != app.MoveStay {
		if next := m.nextToReview(path, move); next >= 0 {
			m.revealFile(m.doc.topOf(m.doc.RowOfFile(next)))
			return
		}
	}
	m.restoreCursor(path)
}

// nextToReview finds the first file below path the pass stops on, or -1 when
// there is none, including when path itself has gone.
//
// What is above path does not come into it — the pass runs down the diff, and a
// file left behind the cursor was left that way on purpose.
func (m *Model) nextToReview(path string, move app.Move) int {
	file := m.fileIndex(path)
	if file < 0 {
		return -1
	}
	for i := file + 1; i < len(m.doc.Files); i++ {
		if m.stopsOn(m.doc.Files[i], move) {
			return i
		}
	}
	return -1
}

// stopsOn reports whether a pass moving on lands on this file.
//
// Under `next-unstaged` the index is what says a file is done: everything of it
// is in there, so there is nothing left to decide about it, and whether it is
// folded is only where it happens to be on screen. A file folded away with work
// still out of the index is a stop — the fold was the reviewer's, and the work
// it hid is work nobody has staged.
//
// Under `next-open` the fold is what says it, whatever the index holds: a file
// staged outside peel has never been read here, so its diff is a stop like any
// other. A session with no index to stage into reads that way too, since there
// the fold is the only record of the pass there is.
func (m *Model) stopsOn(f FileRef, move app.Move) bool {
	if move == app.MoveUnstaged && m.session.Stageable {
		return f.Entry.State() != git.StateStaged
	}
	return !m.collapsed[f.Entry.Path]
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
	// origin is which of the two diffs line counts lines in. A part-staged file
	// has both on screen at once, and the same number names a different line in
	// each, so the note has to record which one it was written on.
	origin store.Origin
	hunk   string
	// end is the last line of the run the note is being written on, when it was
	// written on a run rather than on one line.
	end int
}

func (a anchor) location() string {
	if a.line <= 0 {
		return a.path
	}
	if a.end > a.line {
		return fmt.Sprintf("%s:%d-%d", a.path, a.line, a.end)
	}
	return fmt.Sprintf("%s:%d", a.path, a.line)
}

// rangeEnd is the far end of the run to store with the note, and zero when the
// note is about a single line — which is every note written without marking one.
func (a anchor) rangeEnd() int {
	if a.end > a.line {
		return a.end
	}
	return 0
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
	m.openEditor(got, "", "")
	m.status = "commenting on " + got.location()
}

// editComment opens the editor on the note at the cursor, holding what it says,
// so a note is corrected where it stands rather than deleted and written again.
//
// Only the reviewer's own. An agent's note is signed by the agent, and rewriting
// one would put the reviewer's words behind that name — where `C` would then
// leave them out of the review it hands over, and `X` would clear them with the
// rest of the agent's pass. What to do with a note of the agent's is answer it,
// resolve it or delete it.
func (m *Model) editComment() {
	c, ok := m.commentAtCursor("edit")
	if !ok {
		return
	}
	if c.Author == store.AuthorAgent {
		m.status = "that note is the agent's — x resolves it, D deletes it"
		return
	}
	m.openEditor(anchor{path: c.File, line: c.Line, end: c.EndLine, side: sideOr(c.Side),
		origin: c.Origin, hunk: c.Hunk}, c.ID, c.Body)
	m.status = "editing the comment on " + c.Location()
}

// openEditor puts the editor up at an anchor, holding the text it starts from:
// nothing for a new note, what the note says for one being rewritten.
func (m *Model) openEditor(at anchor, editing, body string) {
	m.pending, m.editing = at, editing
	m.mode = modeComment
	m.input.Reset()
	m.input.SetWidth(m.draftWidth())
	m.input.SetValue(body)
	m.input.SetHeight(m.draftHeight())
	m.input.Focus()

	m.relayout()
	m.revealDraft()
}

// closeComment puts the editor away, leaving the cursor on the code it was
// opened from — or, for an editor opened on a note that has stood in its place
// since, back on the note itself, which is where the cursor was when `e` was
// pressed.
func (m *Model) closeComment() {
	editing := m.editing
	m.input.Blur()
	m.mode = modeBrowse
	m.editing = ""
	// The run stays marked while the note about it is written, and is let go of
	// with the editor: it was marked to write that note, saved or not.
	m.sel = nil
	m.relayout()

	if editing == "" {
		return
	}
	if row := m.doc.RowOfComment(editing); row >= 0 {
		m.moveTo(row)
	}
}

// draft is the comment being written, or the zero draft when nothing is.
func (m *Model) draft() Draft {
	if m.mode != modeComment {
		return Draft{}
	}
	return Draft{anchor: m.pending, Editing: m.editing, Height: m.input.Height()}
}

// draftWidth is the room the editor has, leaving the indent that lines it up
// with the comment bar — and, where the note being written will hang under one
// half of a split row, leaving the other half alone.
func (m *Model) draftWidth() int {
	return max(noteHalf(m.layout, m.pending.line > 0, m.pending.side).editorRoom(m.diffWidth()), minDraftWidth)
}

// minDraftWidth keeps the editor usable in a pane too narrow to line it up with
// the comment it is being written as.
const minDraftWidth = 20

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

// anchorAt resolves the cursor to a comment anchor: the run of lines marked to
// comment on, the line under the cursor, the first changed line of a hunk, an
// existing comment's own anchor, or the file as a whole.
//
// A line anchor does not care whether the line changed, so a note can be left on
// the untouched code a change breaks.
func (m *Model) anchorAt() (anchor, bool) {
	if got, ok := m.markedAnchor(); ok {
		return got, true
	}
	if c, ok := m.doc.CommentAt(m.cursor); ok {
		return anchor{path: c.File, line: c.Line, end: c.EndLine, side: sideOr(c.Side),
			origin: c.Origin, hunk: c.Hunk}, true
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
		return anchor{path: ref.Path, side: store.SideNew, origin: ref.Origin(), hunk: ref.ID.String()}, true
	case TargetFile:
		return anchor{path: target.Path, side: store.SideNew}, true
	default:
		return anchor{}, false
	}
}

// markedAnchor is where a note on the marked run attaches: the two ends of the
// run, counted on one side of the diff.
//
// A note is about one side — the code arriving or the code leaving, not both —
// and the side is the one the run reaches rather than the one it started on.
// Marking a removal and the lines that replaced it is reading a replacement, and
// what a note on a replacement is about is the code that arrived.
//
// The ends are then the first and last numbers the run has on that side, so a
// run passes over the lines the other side owns instead of being cut short by
// them.
//
// A run the reviewer has walked back to a single line is no run at all, and
// falls through to the plain line anchor the cursor already resolves to.
func (m *Model) markedAnchor() (anchor, bool) {
	lo, hi, ok := m.selectedRows()
	if !ok || lo >= hi {
		return anchor{}, false
	}
	ref, index, ok := m.doc.LineAt(lo)
	if !ok {
		return anchor{}, false
	}

	marked := m.markedLines(lo, hi)
	got := lineAnchor(ref, ref.Hunk.Lines[index])
	got.side = runSide(marked)

	got.line = 0
	for _, l := range marked {
		n := lineNumberOn(l, got.side)
		if n <= 0 {
			continue
		}
		if got.line == 0 || n < got.line {
			got.line = n
		}
		got.end = max(got.end, n)
	}
	return got, true
}

// markedLines is the diff's own lines under a run of rows, in the order they are
// read.
func (m *Model) markedLines(lo, hi int) []git.Line {
	var out []git.Line
	for row := lo; row <= hi; row++ {
		r := m.doc.Rows[row]
		if r.Kind != RowLine || r.Hunk < 0 || r.Hunk >= len(m.doc.Hunks) {
			continue
		}
		lines := m.doc.Hunks[r.Hunk].Hunk.Lines
		for _, i := range []int{r.Left, r.Right} {
			if i >= 0 && i < len(lines) {
				out = append(out, lines[i])
			}
		}
	}
	return out
}

// runSide is the side of the diff a run is counted on: the new one as soon as
// the run takes in a line that is arriving, the old one when all it holds of the
// change is code leaving, and the new one for a run of untouched code, whose
// numbers are the current file's.
func runSide(lines []git.Line) store.Side {
	side := store.SideNew
	for _, l := range lines {
		switch l.Kind {
		case git.LineAdded:
			return store.SideNew
		case git.LineRemoved:
			side = store.SideOld
		}
	}
	return side
}

// lineNumberOn is a line's number on one side of the diff, and zero where that
// side does not have the line at all.
func lineNumberOn(l git.Line, side store.Side) int {
	if side == store.SideOld {
		return l.OldLine
	}
	return l.NewLine
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
// comment on a deletion lands on the old file. The diff the line was read from
// is recorded with it: in a part-staged file the index and the working tree both
// have a line 12, and they are not the same line.
func lineAnchor(ref HunkRef, l git.Line) anchor {
	if l.Kind == git.LineRemoved {
		return anchor{path: ref.Path, line: l.OldLine, side: store.SideOld, origin: ref.Origin(), hunk: ref.ID.String()}
	}
	return anchor{path: ref.Path, line: l.NewLine, side: store.SideNew, origin: ref.Origin(), hunk: ref.ID.String()}
}

func sideOr(s store.Side) store.Side {
	if s.Valid() {
		return s
	}
	return store.SideNew
}

func (m *Model) submitComment() tea.Cmd {
	body := strings.TrimSpace(m.input.Value())
	got, editing := m.pending, m.editing
	m.closeComment()
	if editing != "" {
		return m.saveEdit(editing, body)
	}
	if body == "" {
		m.status = "empty comment discarded"
		return nil
	}

	comment := store.Comment{
		File:    got.path,
		Line:    got.line,
		EndLine: got.rangeEnd(),
		Side:    got.side,
		Origin:  got.origin,
		Body:    body,
		Hunk:    got.hunk,
		Author:  store.AuthorUser,
	}
	// The note takes its place in the diff on the keypress, under the code it was
	// written about, with the cursor left on that code: writing a note is not
	// progress through the diff.
	return m.apply(func() {
		shown := comment
		shown.ID = m.unsavedID()
		shown.CreatedAt = time.Now()
		m.comments = withComment(m.comments, shown)
		m.status = "commented on " + got.location()
		m.relayout()
	}, func(ctx context.Context) error {
		_, err := m.backend.AddComment(ctx, comment)
		return err
	})
}

// saveEdit stores what a note says now. Where it is anchored is untouched: the
// note is about the same code it always was, and the reviewer was correcting the
// words rather than pointing it somewhere else.
//
// Emptying the editor is not how a note is deleted. `D` is that key, and it
// says which note it is about to remove first, where an editor cleared with
// ctrl+u would take one away silently.
func (m *Model) saveEdit(id, body string) tea.Cmd {
	c, ok := commentByID(m.comments, id)
	if !ok {
		m.status = "that comment is gone"
		return nil
	}
	if body == "" {
		m.status = "an empty comment is not a way to delete one — D does that"
		return nil
	}
	if body == c.Body {
		m.status = "comment unchanged"
		return nil
	}
	return m.apply(func() {
		m.comments = withBody(m.comments, id, body)
		m.status = "edited the comment on " + c.Location()
		m.relayout()
	}, func(context.Context) error {
		return m.backend.EditComment(id, body)
	})
}

func (m *Model) toggleResolved() tea.Cmd {
	c, ok := m.commentAtCursor("resolve")
	if !ok {
		return nil
	}
	want := !c.Resolved
	done := "resolved " + c.Location()
	if !want {
		done = "reopened " + c.Location()
	}
	return m.apply(func() {
		m.comments = withResolved(m.comments, c.ID, want)
		m.status = done
		m.relayout()
	}, func(context.Context) error {
		return m.backend.SetResolved(c.ID, want)
	})
}

func (m *Model) deleteComment() tea.Cmd {
	c, ok := m.commentAtCursor("delete")
	if !ok {
		return nil
	}
	return m.apply(func() {
		m.comments = withoutComment(m.comments, c.ID)
		m.status = "deleted comment on " + c.Location()
		m.relayout()
	}, func(ctx context.Context) error {
		return m.backend.RemoveComment(ctx, c.ID)
	})
}

// copyComments puts the review on the clipboard as text to paste into an agent.
//
// An agent working in this repository reads the store instead, so what this is
// for is the one that cannot: a browser tab, or another machine. What it hands
// over is the review the person wrote — an agent's notes are what a review was
// already given for, not something to send back out to be addressed — and the
// resolved ones stay behind, since those have been dealt with and an agent asked
// to address them would only redo work already done.
//
// Nothing about the review changes, so there is nothing to queue: the copy is
// reported as done straight away, and only comes back if there was nothing on
// PATH to copy with.
func (m *Model) copyComments() tea.Cmd {
	open, resolved := stillOpen(userComments(m.comments))
	if len(open) == 0 {
		switch {
		case resolved > 0:
			m.status = "every comment of your own is resolved — nothing to copy"
		case len(agentComments(m.comments)) > 0:
			m.status = "no comments of your own to copy — the agent's are left out"
		default:
			m.status = "no comments to copy"
		}
		return nil
	}

	text := commentHandoff(open, m.doc.orphanPaths())
	before := m.snapshot()
	m.status = "copied " + plural(len(open), "comment")
	if resolved > 0 {
		m.status += " — " + plural(resolved, "resolved one") + " left out"
	}
	backend, ctx := m.backend, m.ctx
	return func() tea.Msg {
		if err := backend.Copy(ctx, text); err != nil {
			return revertMsg{err: err, before: before}
		}
		return nil
	}
}

// toggleAgentComments takes the agent's notes out of the diff, or puts them
// back — for reading the code without a review that has already been read
// sitting in it. Nothing is written: `X` is the one that deletes.
func (m *Model) toggleAgentComments() {
	n := len(agentComments(m.comments))
	if n == 0 {
		m.status = "no agent comments"
		return
	}
	m.setAgentCommentsHidden(!m.agentCommentsOff)
	m.relayout()
	if m.agentCommentsOff {
		m.status = plural(n, "agent comment") + " hidden"
		return
	}
	m.status = plural(n, "agent comment") + " shown"
}

// setAgentCommentsHidden takes the agent's notes out of the diff or puts them
// back. It is the only place that changes, so it is also where the choice is
// written down for the next reading of this review.
//
// A choice that fails to persist is worth saying but not worth undoing, the way
// a fold is: the diff on screen is the one that was asked for either way.
func (m *Model) setAgentCommentsHidden(hidden bool) {
	if m.agentCommentsOff == hidden {
		return
	}
	m.agentCommentsOff = hidden
	if err := m.backend.SetAgentCommentsHidden(hidden); err != nil {
		m.err = err
	}
}

// askClearAgentComments puts the deletion to the reviewer before doing it. One
// comment deleted by mistake is a comment to write again; a whole review is
// not, so `X` asks and `D` does not.
func (m *Model) askClearAgentComments() {
	ids := savedIDs(agentComments(m.comments))
	if len(ids) == 0 {
		m.status = "no agent comments to delete"
		return
	}
	m.mode = modeConfirm
	m.ask = &confirm{
		question: "delete " + plural(len(ids), "agent comment") + "?",
		yes:      func() tea.Cmd { return m.clearAgentComments(ids) },
	}
}

// clearAgentComments removes the agent's notes, a store call each behind a
// single change on screen. Hiding them again would say nothing once they are
// gone, so the filter comes off with them.
func (m *Model) clearAgentComments(ids []string) tea.Cmd {
	backend := m.backend
	return m.apply(func() {
		m.comments = withoutComments(m.comments, ids)
		m.setAgentCommentsHidden(false)
		m.status = "deleted " + plural(len(ids), "agent comment")
		m.relayout()
	}, func(ctx context.Context) error {
		for _, id := range ids {
			if err := backend.RemoveComment(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// agentComments picks out the notes an agent left. A comment written here is
// the reviewer's own, so `A` and `X` never reach one.
func agentComments(comments []store.Comment) []store.Comment {
	out := make([]store.Comment, 0, len(comments))
	for _, c := range comments {
		if c.Author == store.AuthorAgent {
			out = append(out, c)
		}
	}
	return out
}

// userComments picks out the reviewer's own notes — what `C` hands over, and
// what is left on screen once `A` hides the rest.
func userComments(comments []store.Comment) []store.Comment {
	out := make([]store.Comment, 0, len(comments))
	for _, c := range comments {
		if c.Author != store.AuthorAgent {
			out = append(out, c)
		}
	}
	return out
}

// visibleComments is the list the document is built from: all of them, until
// the agent's are hidden.
func (m *Model) visibleComments() []store.Comment {
	if !m.agentCommentsOff {
		return m.comments
	}
	return userComments(m.comments)
}

// savedIDs collects the ids of comments the store knows about, leaving out any
// drawn ahead of their own write — those have no id to remove yet.
func savedIDs(comments []store.Comment) []string {
	out := make([]string, 0, len(comments))
	for _, c := range comments {
		if !unsaved(c) {
			out = append(out, c.ID)
		}
	}
	return out
}

// commentAtCursor resolves the comment a key is about to act on.
//
// A comment written a moment ago is on screen before the store has it, and the
// store is what the keys address a comment through — so that one is left alone
// until its write lands, which is the next thing to happen either way.
func (m *Model) commentAtCursor(verb string) (store.Comment, bool) {
	c, ok := m.doc.CommentAt(m.cursor)
	if !ok {
		m.status = "move to a comment to " + verb + " it"
		return store.Comment{}, false
	}
	if unsaved(c) {
		m.status = "that comment is still being saved"
		return store.Comment{}, false
	}
	return c, true
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

// load re-reads the session and its comments.
func load(ctx context.Context, backend Backend, note string) tea.Msg {
	session, err := backend.Reload(ctx)
	if err != nil {
		return errMsg{err}
	}
	comments, err := backend.Comments(ctx)
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
	// reconcile marks the read-back behind a change already on screen. It
	// confirms what the reviewer is looking at rather than telling them
	// something, so it moves nothing and says nothing.
	reconcile bool
}

type walkthroughMsg struct{ body string }

// contextMsg carries the copies of the files the diff is measured against, for
// reading in the unchanged code it leaves out.
type contextMsg struct{ files map[FileSide][]string }

type errMsg struct{ err error }

type tickMsg struct{}

// contextCmd reads those copies behind the screen.
//
// The session it reads is the one on screen now, passed along rather than read
// off the backend, so a reload landing meanwhile cannot swap the files out from
// under it.
//
// A read that fails says nothing. It costs the rows offering to open a run of
// hidden code and nothing else, and a banner over a review because a file could
// not be read a second time would be reporting on something nobody asked for.
func (m *Model) contextCmd() tea.Cmd {
	backend, ctx, session := m.backend, m.ctx, m.session
	return func() tea.Msg {
		files, err := backend.Context(ctx, session)
		if err != nil {
			return nil
		}
		return contextMsg{files: files}
	}
}

// setContext draws the rows that offer to open what the diff left out, now that
// there is something to open them from.
func (m *Model) setContext(msg contextMsg) {
	m.context = msg.files
	m.relayout()
}

// expansion is the code read in around the hunks, as the document takes it.
func (m *Model) expansion() Expansion {
	return Expansion{Files: m.context, Revealed: m.revealed}
}

// expandAt reads in a step's worth of the code the diff left out at the cursor.
//
// The cursor stays on the row it was pressed on, so holding `space` walks
// outwards from the hunk a step at a time. A run finished off has no row left to
// stand on, and the cursor lands on the code that arrived where it was.
func (m *Model) expandAt(at int) {
	ref := m.doc.Expands[at]
	shown := min(expandStep, ref.Hidden)
	m.revealed[ref.ExpandKey] += shown

	row := ref.Row
	m.rebuild()
	if back := m.doc.RowOfExpand(ref.ExpandKey, ref.Dir); back >= 0 {
		m.moveTo(back)
	} else {
		m.moveTo(m.doc.Nearest(min(row, max(m.doc.Len()-1, 0))))
	}
	m.status = "showing " + plural(shown, "line") + " more of " + ref.Path
}

// applyLoaded swaps in a freshly read session, leaving the reviewer on the line
// they were reading rather than jumping them back to the top.
//
// It hands back the read of the files behind it: the diff has moved on, so the
// copies the revealed code came out of have to be taken as moved on too, and a
// load nobody applied leaves them alone.
func (m *Model) applyLoaded(msg loadedMsg) tea.Cmd {
	if msg.reconcile && m.writes.Load() > 0 {
		// A change pressed while this was being read is on screen already: this
		// session was read before it, so applying it would take that change back
		// off. The write behind it brings its own read-back.
		return nil
	}
	if msg.poll && !m.acceptPoll(msg) {
		return nil
	}
	at := m.spot()
	m.session = msg.session
	m.comments = msg.comments
	// The code read in around the hunks came out of files this load has just
	// replaced. What has been asked for is kept — a run opened by hand stays open
	// — but the copies it is drawn from are read again, so nothing on screen is
	// text from a file as it was.
	m.context = nil
	if !msg.reconcile {
		m.busy = ""
		m.err = nil
		// A reload that lands under an open editor takes the keyboard back, so the
		// editor has to be put away with it rather than left focused and invisible.
		// A question waiting for an answer goes the same way: what it was about to
		// delete was read before the reload. So does a search: the files it was
		// listing are the ones this reload has just replaced. And so does a review
		// being written, since the notes it was about to carry have just been read
		// again.
		m.input.Blur()
		m.input.Placeholder = commentPlaceholder
		m.mode, m.ask, m.editing, m.find = modeBrowse, nil, "", finder{}
		m.posting = nil
	}
	// The narrative is kept: it is the notes the reviewer is reading, and
	// dropping it would reorder the diff underneath them every time they staged
	// a hunk. When the code itself moved on it is marked instead, since only the
	// reviewer knows whether waiting for a new one is worth it.
	m.walkStale = m.walkStale || (m.walkLoaded && codeFingerprintOf(msg.session) != m.walkCode)

	m.fingerprint = fingerprintOf(msg.session)
	// Not on a reconciling read-back: that is the reviewer's own stage coming
	// back from git, and a file folded a moment ago by pressing `s` is not a file
	// that has changed since it was staged.
	var reopened []string
	if !msg.reconcile {
		reopened = m.reopenStagedChanged()
	}
	m.rebuild()
	// A reload is not somewhere to be taken: it is the same review with newer
	// text under it, so the cursor stays on the line it was on and falls back
	// through the hunk and the file only when that line has gone.
	m.moveToSpot(at)
	if msg.note != "" {
		m.status = msg.note
	}
	if msg.poll {
		m.status = "reloaded — the working tree changed"
	}
	// A file that had been dealt with is open again, which is a change to the
	// screen nobody asked for: it is worth saying which file, and why.
	if len(reopened) > 0 {
		m.status = "reopened " + strings.Join(reopened, ", ") + " — changed since it was staged"
	}
	return m.contextCmd()
}

// acceptPoll decides whether an unrequested reload is worth applying. Redrawing
// while someone is mid-comment or mid-answer, or when nothing actually changed,
// is worse than waiting for the next tick.
func (m *Model) acceptPoll(msg loadedMsg) bool {
	// A search half typed is a file about to be gone to, the way a comment half
	// written is: relisting what it matches mid-thought is worse than waiting for
	// the next tick, which will still find whatever changed. A review being
	// written is the same, and the one where a redraw would also take away the
	// count of what is about to be sent.
	switch m.mode {
	case modeComment, modeConfirm, modeFind, modeReview, modeReviewEvent:
		return false
	}
	// Lines marked to comment on are a note half written: redrawing the diff
	// under them would take the run away mid-thought, and the next tick will
	// still find whatever changed.
	if m.sel != nil {
		return false
	}
	// A change of the reviewer's own is on screen and still being written: this
	// poll may have read git before it landed, and would undraw it.
	if m.writes.Load() > 0 {
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
	m.doc = Build(m.session, m.visibleComments(), m.collapsed, m.layout,
		WithGroups(m.groups()), WithDraft(m.draft()), WithSideFolds(m.sideFolds),
		WithPaneWidth(m.diffWidth()), WithExpansion(m.expansion()))
	m.fileRows = fileTree(m.doc.Files)
	if m.cursor >= m.doc.Len() {
		m.cursor = m.doc.LastStop()
	}
	m.renderer.SetWidth(m.diffWidth())
	m.clampTop()
	m.clampFileTop()
	m.clampCode()
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

func (m *Model) toggleFilePane() {
	m.filePaneOff = !m.filePaneOff
	m.relayout()
	if m.filePaneOff {
		m.status = "file tree hidden"
		return
	}
	m.ensureFileVisible()
	m.status = "file tree shown"
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

// toggleCollapse opens or puts away whatever the cursor is on: a walkthrough
// step's explanation, one half of a part-staged file, a run of code the diff
// left out, or a file's body.
//
// One key for all of them because they are one question — is this worth having
// on screen — asked of whatever the cursor names. On a row standing where code
// was left out the answer runs the other way: there is more of the file to read,
// and this shows it.
//
// Folding a file means the same thing staging one does — done with it — so the
// cursor moves on, by its own setting: `peel.afterFold`, which defaults to the
// next file with work still out of the index. Opening one again leaves the
// cursor on it, since that is the file being read.
func (m *Model) toggleCollapse() {
	if step := m.doc.StepAt(m.cursor); step >= 0 {
		m.foldStep(step)
		return
	}
	if side := m.doc.SideAt(m.cursor); side >= 0 {
		m.foldSide(side)
		return
	}
	if at := m.doc.ExpandAt(m.cursor); at >= 0 {
		m.expandAt(at)
		return
	}
	file := m.doc.FileAt(m.cursor)
	if file < 0 || file >= len(m.doc.Files) {
		return
	}
	path := m.doc.Files[file].Entry.Path
	if !m.collapsed[path] {
		// A file put away by hand is put away for good: it was read and closed,
		// where a file folded by staging opens again when something lands in it.
		delete(m.stagedFolds, path)
		m.foldNow(path, m.moves.AfterFold)
		return
	}
	m.setCollapsed(unfold(path))
	m.rebuild()
	m.moveTo(m.doc.RowOfFile(file))
}

// foldSide hides or shows one half of a part-staged file, leaving the cursor on
// the heading it was pressed on.
//
// Only the index half can be hidden: the working tree's is the change still to
// be reviewed, and folding that away would leave a file that says it has changes
// and shows none.
func (m *Model) foldSide(side int) {
	ref := m.doc.Sides[side]
	if !ref.Staged {
		m.status = "only the staged half folds — this is what is left to review"
		return
	}
	m.sideFolds[ref.Path] = !ref.Folded
	m.rebuild()
	if row := m.doc.RowOfSide(ref.Path, ref.Origin()); row >= 0 {
		m.moveTo(row)
	}
	if ref.Folded {
		m.status = "showing what is already staged in " + ref.Path
	} else {
		m.status = "hiding what is already staged in " + ref.Path
	}
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

// revealFile puts the cursor on the top of the file the pass has moved on to
// after folding or staging the one before it, and moves the window only when
// that row is off screen. Folding a file pulls what is below it up, so the file
// the pass carries on with is often already in front of the reviewer — jumping
// it to the first row would throw away the diff they are still looking at to
// show them something they can already see.
//
// When the window does have to move it stops as soon as the file's first
// revealLines rows are on screen, rather than carrying on until its header is at
// the top. The file arrives at the bottom of the window with what came before it
// still above, which is how it would have arrived had the reviewer scrolled
// there.
func (m *Model) revealFile(row int) {
	if row < 0 || row >= m.doc.Len() {
		return
	}
	m.cursor = row
	if height := m.bodyHeight(); row < m.top || row >= m.top+height {
		m.top = row - max(height-revealLines, 0)
	}
	m.clampTop()
	m.ensureFileVisible()
}

// revealLines is how much of a file revealFile brings on screen when the window
// has to move: enough to read the header and start on the first hunk, and few
// enough that the file lands at the bottom of the window rather than filling it.
const revealLines = 12

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

// codeStep is how far one press of h or l slides the code: one tab stop, so a
// press moves the code by an indent level rather than by an arbitrary distance.
const codeStep = tabWidth

// scrollCode slides the code sideways under a pinned gutter, for a line too long
// for the pane.
//
// Unlike the vertical window this does not touch the cursor: the same rows are
// on screen either way, so there is nothing to drag it back to.
func (m *Model) scrollCode(delta int) {
	m.xoff += delta
	m.clampCode()
}

func (m *Model) clampCode() {
	m.xoff = min(max(m.xoff, 0), m.maxCodeOffset())
	m.renderer.SetOffset(m.xoff)
}

// maxCodeOffset stops the code sliding past its own longest line, so scrolling
// right can never leave the pane blank with nothing to scroll back to.
func (m *Model) maxCodeOffset() int {
	return max(0, m.doc.CodeWidth-m.renderer.CodeColumns(m.layout))
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

// markedPath is the path of the file the pane marks, or "" when there is none.
// The pane addresses its rows by path, since a row of the tree is a directory
// as often as it is a file.
func (m *Model) markedPath() string {
	file := m.markedFile()
	if file < 0 || file >= len(m.doc.Files) {
		return ""
	}
	return m.doc.Files[file].Entry.Path
}

// ensureFileVisible scrolls the pane only when the marked file has left it, so
// a pane scrolled by hand stays where it was put.
func (m *Model) ensureFileVisible() {
	height := m.bodyHeight()
	row := m.paneRowOf(m.markedFile())
	if row < 0 || height <= 0 {
		return
	}
	if row < m.fileTop {
		m.fileTop = row
	}
	if row >= m.fileTop+height {
		m.fileTop = row - height + 1
	}
	m.clampFileTop()
}

// paneRowOf is where a file sits in the pane, which is past its own index once
// the directories above it have rows of their own.
func (m *Model) paneRowOf(file int) int {
	if file < 0 {
		return -1
	}
	for i, row := range m.fileRows {
		if row.File == file {
			return i
		}
	}
	return -1
}

func (m *Model) clampFileTop() {
	m.fileTop = min(max(m.fileTop, 0), max(0, len(m.fileRows)-m.bodyHeight()))
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
	if m.mode == modeReview {
		m.input.SetWidth(m.reviewWidth())
	}
	// A walkthrough's explanations and a comment's text are both wrapped into
	// rows, so a resize changes how many rows the document has.
	if len(m.doc.Steps) > 0 || len(m.doc.Comments) > 0 {
		m.relayout()
	}
	m.clampTop()
	m.clampCode()
	m.ensureVisible(m.cursor)
	m.ensureFileVisible()
	m.revealDraft()
}
