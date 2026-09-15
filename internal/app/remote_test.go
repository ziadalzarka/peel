package app_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/store"
)

func TestOpeningAPullRequestBringsInTheCommentsOnIt(t *testing.T) {
	f := newFixture(t)
	f.gh.comments = []forge.RemoteComment{
		{ID: "11", Path: "pr.go", Line: 1, Side: "RIGHT", Body: "why new", Author: "octocat", Resolved: true},
		{ID: "12", Path: "pr.go", Line: 1, Side: "LEFT", Body: "why old", Author: "hubot"},
	}

	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}
	if s.CommentsErr != nil {
		t.Fatalf("CommentsErr = %v", s.CommentsErr)
	}

	all, err := f.app.StateFor(s).Comments.List(s.CommentFilter())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[string]store.Comment{}
	for _, c := range all {
		byID[c.ID] = c
	}
	if c := byID["fake-forge-11"]; c.Author != "octocat" || !c.Resolved || c.Side != store.SideNew || c.Remote != "fake-forge" {
		t.Errorf("fake-forge-11 = %+v, want octocat's resolved note on the new side", c)
	}
	if c := byID["fake-forge-12"]; c.Author != "hubot" || c.Resolved || c.Side != store.SideOld {
		t.Errorf("fake-forge-12 = %+v, want hubot's open note on the old side", c)
	}
}

func TestAPullRequestsOwnCommentsAreNotPostedBack(t *testing.T) {
	f := newFixture(t)
	f.gh.comments = []forge.RemoteComment{
		{ID: "11", Path: "pr.go", Line: 1, Side: "RIGHT", Body: "from the pull request", Author: "octocat"},
	}
	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v", err)
	}
	mustAddComment(t, f, store.Comment{File: "pr.go", Line: 1, Body: "mine", Target: s.Target})

	if _, err := f.app.SubmitReview(f.ctx, s, app.SubmitOptions{}); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if got := f.gh.submitted[0].Comments; len(got) != 1 || got[0].Body != "mine" {
		t.Errorf("submitted %+v, want only the note written here", got)
	}
}

func TestAPullRequestOpensWhenItsCommentsCannotBeRead(t *testing.T) {
	f := newFixture(t)
	f.gh.commentsErr = errors.New("rate limited")

	s, err := f.app.LoadPullRequest(f.ctx, "", "412")
	if err != nil {
		t.Fatalf("LoadPullRequest: %v, want the review to open anyway", err)
	}
	if s.CommentsErr == nil || !strings.Contains(s.CommentsErr.Error(), "rate limited") {
		t.Errorf("CommentsErr = %v, want the failed read named", s.CommentsErr)
	}
}
