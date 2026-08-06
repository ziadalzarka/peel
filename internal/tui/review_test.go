package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/store"
)

// prSession is a pull request being reviewed: the same diff the other tests
// use, read-only, with a pull request behind it.
func prSession(t *testing.T) *app.Session {
	t.Helper()
	s := newSession(t, twoFileDiff)
	s.Title = "#412 Drop the document key"
	s.Target = "github:cli/cli#412"
	s.Stageable = false
	s.PR = &forge.PullRequest{
		Ref:   forge.Ref{Owner: "cli", Repo: "cli", Number: 412},
		Title: "Drop the document key",
		URL:   "https://github.com/cli/cli/pull/412",
	}
	return s
}

// prModel is a review of a pull request with one note already on it.
func prModel(t *testing.T) (*fakeBackend, *Model) {
	t.Helper()
	backend := newFakeBackend(prSession(t))
	backend.comments = []store.Comment{
		{ID: "c1", File: "alpha.go", Line: 3, Body: "this leaks the tx", Author: store.AuthorUser},
	}
	return backend, newModel(t, backend)
}

// The whole flow: a summary, what the review does, the question, and only then
// does anything leave the machine.
func TestPostingAReviewAsksBeforeItSends(t *testing.T) {
	backend, m := prModel(t)

	press(t, m, "P")
	if m.mode != modeReview {
		t.Fatalf("mode = %v, want the summary editor", m.mode)
	}
	typeText(t, m, "a couple of things")

	press(t, m, "enter")
	if m.mode != modeReviewEvent {
		t.Fatalf("mode = %v, want the choice of what posting does", m.mode)
	}
	if len(backend.posted) != 0 {
		t.Fatal("the review went out before it was chosen")
	}

	press(t, m, "r")
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want the question", m.mode)
	}
	if len(backend.posted) != 0 {
		t.Fatal("the review went out before the question was answered")
	}
	if want := "post 1 comment to cli/cli#412 as request changes?"; m.ask.question != want {
		t.Errorf("question = %q, want %q", m.ask.question, want)
	}

	press(t, m, "y")
	if len(backend.posted) != 1 {
		t.Fatalf("posted %d reviews, want 1", len(backend.posted))
	}
	got := backend.posted[0]
	if got.Body != "a couple of things" {
		t.Errorf("Body = %q", got.Body)
	}
	if got.Event != forge.EventRequestChanges {
		t.Errorf("Event = %q", got.Event)
	}
	if len(got.Comments) != 1 || got.Comments[0].Body != "this leaks the tx" {
		t.Errorf("Comments = %v", got.Comments)
	}
	if !strings.Contains(m.status, "posted 1 comment to cli/cli#412") {
		t.Errorf("status = %q, want it to say what went where", m.status)
	}
	// The notes that went are the other side's problem now, so the diff reads
	// back with them resolved.
	if !m.comments[0].Resolved {
		t.Error("the posted comment is still open on screen")
	}
}

func TestPostingChoosesApproveOrComment(t *testing.T) {
	for _, tc := range []struct {
		key   string
		event forge.ReviewEvent
		named string
	}{
		{"a", forge.EventApprove, "approve"},
		{"c", forge.EventComment, "comment"},
	} {
		t.Run(tc.named, func(t *testing.T) {
			backend, m := prModel(t)

			press(t, m, "P", "enter", tc.key)
			if !strings.Contains(m.ask.question, "as "+tc.named+"?") {
				t.Errorf("question = %q, want it to name %s", m.ask.question, tc.named)
			}
			press(t, m, "y")

			if len(backend.posted) != 1 || backend.posted[0].Event != tc.event {
				t.Fatalf("posted = %v, want one %s", backend.posted, tc.event)
			}
			if backend.posted[0].Body != "" {
				t.Errorf("Body = %q, want none — a review can be its comments alone", backend.posted[0].Body)
			}
		})
	}
}

// Every step before the last is a way out, and none of them sends anything.
func TestPostingCanBeAbandonedAtEveryStep(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
	}{
		{"in the summary", []string{"P", "esc"}},
		{"at the choice", []string{"P", "enter", "esc"}},
		{"at the question", []string{"P", "enter", "a", "n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, m := prModel(t)
			press(t, m, tc.keys...)

			if len(backend.posted) != 0 {
				t.Fatalf("posted %v after backing out", backend.posted)
			}
			if m.mode != modeBrowse {
				t.Errorf("mode = %v, want browse", m.mode)
			}
			if m.posting != nil {
				t.Error("the review being written was left behind")
			}
			if m.input.Placeholder != commentPlaceholder {
				t.Errorf("placeholder = %q, want the comment editor's back", m.input.Placeholder)
			}
		})
	}
}

// P is a pull request key. On a working tree there is nowhere to post to, and
// saying so is better than a panel that cannot do anything.
func TestPostingNeedsAPullRequest(t *testing.T) {
	backend := newFakeBackend(newSession(t, twoFileDiff))
	m := newModel(t, backend)

	press(t, m, "P")

	if m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse", m.mode)
	}
	if !strings.Contains(m.status, "--pr") {
		t.Errorf("status = %q, want it to say how to open one", m.status)
	}
}

// A review with nothing in it is refused while it can still be added to, rather
// than after the reviewer has said yes to sending it.
func TestPostingRefusesAnEmptyReviewBeforeTheQuestion(t *testing.T) {
	backend := newFakeBackend(prSession(t))
	m := newModel(t, backend)

	press(t, m, "P", "enter", "c")

	if m.mode == modeConfirm {
		t.Fatal("an empty review reached the question")
	}
	if m.err == nil {
		t.Fatal("an empty review was accepted silently")
	}
	if len(backend.posted) != 0 {
		t.Errorf("posted %v", backend.posted)
	}
}

// A post that fails says so, and takes nothing back off the screen: the notes
// are still open, because they never went.
func TestPostingReportsAFailure(t *testing.T) {
	backend, m := prModel(t)
	backend.postErr = errors.New("HTTP 422")

	press(t, m, "P", "enter", "c", "y")

	if m.err == nil || !strings.Contains(m.err.Error(), "422") {
		t.Errorf("err = %v, want the failure", m.err)
	}
	if m.busy != "" {
		t.Errorf("busy = %q, want it cleared", m.busy)
	}
	if m.comments[0].Resolved {
		t.Error("a comment was resolved by a review that never posted")
	}
}

// The panel says what is about to go and where, since that is the part the diff
// behind it does not.
func TestReviewPanelSaysWhatIsAboutToGo(t *testing.T) {
	backend, m := prModel(t)
	backend.comments = append(backend.comments,
		store.Comment{ID: "c2", File: "alpha.go", Body: "the file as a whole", Author: store.AuthorUser})
	m.comments = backend.comments

	press(t, m, "P")
	view := m.View()

	for _, want := range []string{"post review", "cli/cli#412", "1 comment", "cannot be posted inline"} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel does not say %q:\n%s", want, view)
		}
	}
}

func TestReviewPanelShowsTheChoices(t *testing.T) {
	_, m := prModel(t)

	press(t, m, "P")
	typeText(t, m, "looks good")
	press(t, m, "enter")

	view := m.View()
	for _, want := range []string{"looks good", "approve", "request changes"} {
		if !strings.Contains(view, want) {
			t.Errorf("the panel does not say %q:\n%s", want, view)
		}
	}
}
