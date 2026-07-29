// Package forge reads pull requests from code hosts and submits reviews back.
//
// Only GitHub ships today, via the `gh` CLI. The interface exists so another
// host can be added without touching the TUI or the CLI.
package forge

import (
	"context"
	"fmt"
	"strings"

	"github.com/ziadalzarka/peel/internal/registry"
)

// ErrNoProvider reports that no registered forge is usable right now.
var ErrNoProvider = registry.ErrNoneAvailable

// Ref identifies one pull request.
type Ref struct {
	Owner  string
	Repo   string
	Number int
}

// Slug renders "owner/repo".
func (r Ref) Slug() string { return r.Owner + "/" + r.Repo }

// String renders "owner/repo#123".
func (r Ref) String() string { return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.Number) }

// Valid reports whether the ref is complete enough to use.
func (r Ref) Valid() bool { return r.Owner != "" && r.Repo != "" && r.Number > 0 }

// Target renders the value stored on comments to scope them to this pull
// request, e.g. "github:cli/cli#123".
func (r Ref) Target(provider string) string { return provider + ":" + r.String() }

// PullRequest is the reviewable state of one pull request.
type PullRequest struct {
	Ref     Ref
	Title   string
	Body    string
	Author  string
	BaseRef string
	HeadRef string
	URL     string
	State   string
	Draft   bool
	// Diff is the unified diff of the whole pull request.
	Diff string
}

// Describe renders a one-line summary for display.
func (p PullRequest) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d %s", p.Ref.Number, p.Title)
	if p.Author != "" {
		fmt.Fprintf(&b, " · %s", p.Author)
	}
	if p.Draft {
		b.WriteString(" · draft")
	}
	return b.String()
}

// ReviewEvent is what submitting a review does to it.
type ReviewEvent string

const (
	// EventComment leaves the review without approving or blocking.
	EventComment ReviewEvent = "COMMENT"
	// EventApprove approves the pull request.
	EventApprove ReviewEvent = "APPROVE"
	// EventRequestChanges blocks the pull request.
	EventRequestChanges ReviewEvent = "REQUEST_CHANGES"
)

// Valid reports whether e is a recognised event.
func (e ReviewEvent) Valid() bool {
	switch e {
	case EventComment, EventApprove, EventRequestChanges:
		return true
	}
	return false
}

// ReviewComment is one inline note to post.
type ReviewComment struct {
	Path string
	// Line is the line number in the file, on Side.
	Line int
	// Side is "RIGHT" for the changed file or "LEFT" for the original.
	Side string
	Body string
}

// Review is a batch of notes submitted together.
type Review struct {
	Body     string
	Event    ReviewEvent
	Comments []ReviewComment
}

// Validate reports whether the review can be submitted.
func (r Review) Validate() error {
	if !r.Event.Valid() {
		return fmt.Errorf("unknown review event %q", r.Event)
	}
	if strings.TrimSpace(r.Body) == "" && len(r.Comments) == 0 {
		return fmt.Errorf("review is empty: needs a body or at least one comment")
	}
	for i, c := range r.Comments {
		if strings.TrimSpace(c.Path) == "" {
			return fmt.Errorf("comment %d: path is required", i+1)
		}
		if strings.TrimSpace(c.Body) == "" {
			return fmt.Errorf("comment %d on %s: body is required", i+1, c.Path)
		}
		if c.Line <= 0 {
			return fmt.Errorf("comment %d on %s: line must be positive, got %d", i+1, c.Path, c.Line)
		}
		if c.Side != "" && c.Side != "RIGHT" && c.Side != "LEFT" {
			return fmt.Errorf("comment %d on %s: side must be RIGHT or LEFT, got %q", i+1, c.Path, c.Side)
		}
	}
	return nil
}

// Provider reads pull requests from one code host.
type Provider interface {
	registry.Provider

	// Parse turns a user-supplied reference — a number, an owner/repo#number,
	// or a URL — into a Ref. A bare number resolves against the repository at
	// dir.
	Parse(ctx context.Context, dir, ref string) (Ref, error)
	// Fetch returns the pull request and its diff.
	Fetch(ctx context.Context, ref Ref) (*PullRequest, error)
	// SubmitReview posts a review. This is the one outward-facing operation in
	// peel, so callers must confirm with the user before invoking it.
	SubmitReview(ctx context.Context, ref Ref, review Review) error
}

// Registry holds the known forge providers and picks between them.
type Registry = registry.Registry[Provider]

// NewRegistry returns a registry containing the given providers, in preference
// order.
func NewRegistry(providers ...Provider) *Registry {
	return registry.New("forge provider", providers...)
}
