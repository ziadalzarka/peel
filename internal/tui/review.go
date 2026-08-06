package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/forge"
)

// Posting a review is the one thing peel does that anyone else can see, and the
// one thing it cannot take back. So `P` is three deliberate steps rather than a
// key that sends: the summary is written, what the review does to the pull
// request is chosen, and the question in the footer says how many notes are
// about to go where before anything leaves the machine.
//
// The notes themselves are already on screen — they are what the reviewer has
// been reading — so the payload is not shown a second time. What the question
// adds is the part that is not on screen: the count, the destination, and the
// verdict.

// posting is a review on its way out: the summary written for it and what it
// does to the pull request. It is held from the editor until the question is
// answered, since the editor has been put away by then.
type posting struct {
	body  string
	event forge.ReviewEvent
}

// reviewPlaceholder is what the summary editor says before anything is written.
// A review can be nothing but its comments, so it says the summary is optional.
const reviewPlaceholder = "Summary for the review (optional)…"

// postBusyNote is the banner shown while the review is being posted. It doubles
// as the guard against a second `P` starting a second post.
const postBusyNote = "posting review"

// openReview starts the summary for a review of the pull request being read.
func (m *Model) openReview() {
	if m.session.PR == nil {
		m.status = "P posts a review to a pull request — peel --pr <ref> opens one"
		return
	}
	if m.busy == postBusyNote {
		m.status = "already posting a review"
		return
	}
	inline, fileLevel := m.pendingReview()
	if inline == 0 && fileLevel == 0 {
		m.status = "no open comments to post — c writes one, or write a summary and post that"
	}

	m.mode = modeReview
	m.posting = &posting{}
	m.input.Placeholder = reviewPlaceholder
	m.input.Reset()
	m.input.SetWidth(m.reviewWidth())
	m.input.SetHeight(draftMinHeight)
	m.input.Focus()
}

// closeReview puts the panel away, whether the review went or was thought
// better of.
func (m *Model) closeReview() {
	m.input.Blur()
	m.input.Reset()
	m.input.Placeholder = commentPlaceholder
	m.mode = modeBrowse
	m.posting = nil
}

// reviewKey drives the summary: enter moves on to what the review does, and the
// rest is typing — including the shift+enter that writes a second line, as in
// the comment editor.
func (m *Model) reviewKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.closeReview()
		m.status = "review cancelled — nothing was posted"
		return nil
	case "enter":
		m.posting.body = strings.TrimSpace(m.input.Value())
		m.input.Blur()
		m.mode = modeReviewEvent
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.input.SetHeight(m.draftHeight())
	return cmd
}

// reviewEventKey chooses what posting does to the pull request, and puts the
// question that sends it.
func (m *Model) reviewEventKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc":
		m.closeReview()
		m.status = "review cancelled — nothing was posted"
		return nil
	case "a":
		return m.askPost(forge.EventApprove)
	case "r":
		return m.askPost(forge.EventRequestChanges)
	case "c":
		return m.askPost(forge.EventComment)
	}
	return nil
}

// askPost puts the last question before the review leaves the machine: what is
// about to go, where, and as what.
//
// The payload is built here rather than at the keypress that sends it, so a
// review with nothing postable in it is refused while it can still be added to
// rather than after the reviewer has said yes.
func (m *Model) askPost(event forge.ReviewEvent) tea.Cmd {
	want := posting{body: m.posting.body, event: event}
	review, err := m.backend.ReviewPayload(want.body, want.event)
	if err != nil {
		m.closeReview()
		m.err = err
		return nil
	}

	m.closeReview()
	m.mode = modeConfirm
	m.ask = &confirm{
		question: postQuestion(review, m.prRef()),
		yes:      func() tea.Cmd { return m.postReview(want) },
	}
	return nil
}

// postQuestion is what the footer asks before anything is sent.
func postQuestion(review forge.Review, ref string) string {
	return "post " + carried(review) + " to " + ref + " as " + eventName(review.Event) + "?"
}

// prRef names the pull request being reviewed, for the sentences about it.
func (m *Model) prRef() string {
	if m.session == nil || m.session.PR == nil {
		return "the pull request"
	}
	return m.session.PR.Ref.String()
}

// postReview sends it, and reads the review back: the notes that went are
// resolved now, and what is on screen should say so.
func (m *Model) postReview(want posting) tea.Cmd {
	m.busy = postBusyNote
	backend, ctx, ref := m.backend, m.ctx, m.prRef()
	return func() tea.Msg {
		review, err := backend.SubmitReview(ctx, want.body, want.event)
		if err != nil {
			return errMsg{err}
		}

		note := postedNote(review, ref)
		msg := load(ctx, backend, note)
		if _, ok := msg.(loadedMsg); !ok {
			// The review went out and only reading the notes back failed. That is
			// worth saying, and not worth taking the report of a posted review
			// off the screen for.
			return postedMsg{note: note}
		}
		return msg
	}
}

// postedMsg reports a review that went out when the read-back behind it did
// not.
type postedMsg struct{ note string }

// postedNote is what the footer says once the review has gone.
func postedNote(review forge.Review, ref string) string {
	return "posted " + carried(review) + " to " + ref + " · " + eventName(review.Event)
}

// carried is what a review is made of, for the sentence asking about it and the
// one reporting it. A review with no inline notes is its summary.
func carried(review forge.Review) string {
	if len(review.Comments) == 0 {
		return "a summary"
	}
	return plural(len(review.Comments), "comment")
}

// eventName is what a review event is called in the UI, in the words the keys
// that choose it are labelled with.
func eventName(event forge.ReviewEvent) string {
	switch event {
	case forge.EventApprove:
		return "approve"
	case forge.EventRequestChanges:
		return "request changes"
	default:
		return "comment"
	}
}

// pendingReview counts what a review would carry: the notes still open on this
// review, and the ones written on a file rather than a line — those have
// nowhere inline to attach, so they are said to be staying behind rather than
// counted as going.
//
// A note drawn ahead of its own write is left out: it has no id in the store
// yet, so it is not one the payload can be built from.
func (m *Model) pendingReview() (inline, fileLevel int) {
	for _, c := range m.comments {
		if c.Resolved || unsaved(c) {
			continue
		}
		if c.Line > 0 {
			inline++
			continue
		}
		fileLevel++
	}
	return inline, fileLevel
}

// reviewWidth is the room the summary editor has in the panel.
func (m *Model) reviewWidth() int { return max(m.width-2, minDraftWidth) }

// reviewLines draws the panel `P` puts up: what is about to be posted, and the
// summary being written to go with it.
//
// It is drawn over the foot of the diff rather than on a screen of its own, so
// the notes about to be sent stay in front of the reviewer writing the summary
// that introduces them.
func (m *Model) reviewLines(width int) []string {
	head := " " + m.theme.Title.Render("post review")
	if pr := m.session.PR; pr != nil {
		head += "  " + m.theme.Header.Render(pr.Ref.String())
	}

	inline, fileLevel := m.pendingReview()
	count := m.theme.Comment.Render(plural(inline, "comment"))
	if inline == 0 {
		count = m.theme.Note.Render("no comments — the summary is the review")
	}
	if fileLevel > 0 {
		count += "  " + m.theme.Partial.Render(plural(fileLevel, "file note")+" cannot be posted inline")
	}

	lines := []string{
		m.theme.Dim.Render(strings.Repeat("─", max(width, 0))),
		spread(head, count+" ", width),
	}
	if m.mode == modeReview {
		for _, line := range strings.Split(m.input.View(), "\n") {
			lines = append(lines, fit(" "+line, width))
		}
		return lines
	}
	return append(lines, m.reviewSummaryLines(width)...)
}

// reviewSummaryLines is the panel once the summary is written: what it says,
// and the three things posting can do.
func (m *Model) reviewSummaryLines(width int) []string {
	body := m.theme.Note.Render("no summary")
	if m.posting != nil && m.posting.body != "" {
		body = m.theme.Comment.Render(firstLine(m.posting.body))
	}
	choices := []string{
		m.theme.Key.Render("a") + " approve",
		m.theme.Key.Render("r") + " request changes",
		m.theme.Key.Render("c") + " comment",
	}
	return []string{
		fit(" "+body, width),
		fit(" "+m.theme.Dim.Render("post as")+"  "+strings.Join(choices, m.theme.Dim.Render(" · ")), width),
	}
}

// firstLine is the summary as one row of the panel, saying that there is more
// of it when there is.
func firstLine(s string) string {
	line, rest, cut := strings.Cut(s, "\n")
	if cut && strings.TrimSpace(rest) != "" {
		return line + " …"
	}
	return line
}
