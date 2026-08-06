package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/store"
)

// aiRequest builds the provider request describing a session.
func aiRequest(s *Session, instruction string) ai.Request {
	target := s.Title
	if s.PR != nil {
		target = fmt.Sprintf("pull request %s — %s", s.PR.Ref, s.PR.Title)
	}
	return ai.Request{
		Diff:        s.DiffText,
		Target:      target,
		Files:       s.Paths(),
		Instruction: instruction,
	}
}

// SubmitOptions controls posting a review to a code host.
type SubmitOptions struct {
	// Provider names the forge, or is empty for the first available.
	Provider string
	// Body is the review's summary comment.
	Body string
	// Event decides whether the review comments, approves or blocks.
	Event forge.ReviewEvent
	// ResolveAfter marks the submitted comments resolved locally, so they stop
	// appearing as outstanding work.
	ResolveAfter bool
}

// SubmitReview posts the session's local comments to the code host and returns
// what it sent.
//
// This is the only operation in peel that sends anything off the machine.
// It never runs implicitly: the caller confirms with the user first, and
// PreviewSubmission exists so there is something concrete to confirm against.
func (a *App) SubmitReview(ctx context.Context, s *Session, opts SubmitOptions) (forge.Review, error) {
	if s.PR == nil {
		return forge.Review{}, fmt.Errorf("no pull request in this session: run peel --pr <number> first")
	}
	provider, err := a.Forges.Resolve(ctx, opts.Provider)
	if err != nil {
		return forge.Review{}, err
	}

	review, submitted, err := a.buildReview(s, opts)
	if err != nil {
		return forge.Review{}, err
	}
	if err := provider.SubmitReview(ctx, s.PR.Ref, review); err != nil {
		return forge.Review{}, err
	}

	if !opts.ResolveAfter {
		return review, nil
	}
	comments := a.StateFor(s).Comments
	for _, c := range submitted {
		if _, err := comments.Update(c.ID, func(c *store.Comment) { c.Resolved = true }); err != nil {
			return review, fmt.Errorf("review posted, but marking %s resolved failed: %w", c.ID, err)
		}
	}
	return review, nil
}

// PreviewSubmission returns exactly what SubmitReview would post, so the user
// can see it before agreeing to publish.
func (a *App) PreviewSubmission(s *Session, opts SubmitOptions) (forge.Review, error) {
	review, _, err := a.buildReview(s, opts)
	return review, err
}

// buildReview turns unresolved local comments into a forge review, returning
// both the payload and the comments it came from.
func (a *App) buildReview(s *Session, opts SubmitOptions) (forge.Review, []store.Comment, error) {
	if s.PR == nil {
		return forge.Review{}, nil, fmt.Errorf("no pull request in this session")
	}

	filter := s.CommentFilter()
	filter.Unresolved = true
	comments, err := a.StateFor(s).Comments.List(filter)
	if err != nil {
		return forge.Review{}, nil, err
	}

	event := opts.Event
	if event == "" {
		event = forge.EventComment
	}
	review := forge.Review{Body: opts.Body, Event: event}

	var included []store.Comment
	var skipped []string
	for _, c := range comments {
		// A review comment must anchor to a line in the diff; a file-level note
		// has nowhere to attach, so it is reported rather than dropped quietly.
		if c.Line <= 0 {
			skipped = append(skipped, c.Location())
			continue
		}
		side := "RIGHT"
		if c.Side == store.SideOld {
			side = "LEFT"
		}
		review.Comments = append(review.Comments, forge.ReviewComment{
			Path: c.File,
			Line: c.Line,
			Side: side,
			Body: c.Body,
		})
		included = append(included, c)
	}

	if len(skipped) > 0 && len(review.Comments) == 0 && strings.TrimSpace(review.Body) == "" {
		return forge.Review{}, nil, fmt.Errorf(
			"nothing to submit: %d file-level comment(s) (%s) cannot be posted inline; add a review body with --body",
			len(skipped), strings.Join(skipped, ", "))
	}
	if err := review.Validate(); err != nil {
		return forge.Review{}, nil, err
	}
	return review, included, nil
}
