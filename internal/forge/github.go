package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ziadalzarka/peel/internal/exec"
)

// GitHubProvider reads pull requests through the `gh` CLI.
//
// Shelling out to `gh` rather than calling the REST API directly means peel
// owns no authentication code: whatever `gh auth` is already configured with
// is what peel uses, including enterprise hosts and SSO.
type GitHubProvider struct {
	runner   exec.Runner
	binary   string
	lookPath func(string) bool
}

// GitHubOption configures a GitHubProvider.
type GitHubOption func(*GitHubProvider)

// WithGHBinary overrides the executable name, for tests or a custom install.
func WithGHBinary(name string) GitHubOption {
	return func(p *GitHubProvider) { p.binary = name }
}

// WithGHLookPath overrides binary detection, for tests.
func WithGHLookPath(fn func(string) bool) GitHubOption {
	return func(p *GitHubProvider) { p.lookPath = fn }
}

// NewGitHub returns a provider backed by the `gh` CLI.
func NewGitHub(runner exec.Runner, opts ...GitHubOption) *GitHubProvider {
	p := &GitHubProvider{runner: runner, binary: "gh", lookPath: exec.LookPath}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Name implements Provider.
func (p *GitHubProvider) Name() string { return "github" }

// Description implements Provider.
func (p *GitHubProvider) Description() string {
	return "reads pull requests through the `gh` CLI, using your existing gh auth"
}

// Available implements Provider.
func (p *GitHubProvider) Available(context.Context) bool { return p.lookPath(p.binary) }

// urlPattern matches a pull request URL on github.com or an enterprise host.
var urlPattern = regexp.MustCompile(`^https?://[^/]+/([^/]+)/([^/]+)/pull/(\d+)`)

// slugPattern matches "owner/repo#123" and "owner/repo/123".
var slugPattern = regexp.MustCompile(`^([^/\s]+)/([^/#\s]+)[#/](\d+)$`)

// Parse implements Provider.
func (p *GitHubProvider) Parse(ctx context.Context, dir, ref string) (Ref, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Ref{}, fmt.Errorf("no pull request given")
	}

	if m := urlPattern.FindStringSubmatch(ref); m != nil {
		n, err := strconv.Atoi(m[3])
		if err != nil {
			return Ref{}, fmt.Errorf("pull request %q: %w", ref, err)
		}
		return Ref{Owner: m[1], Repo: strings.TrimSuffix(m[2], ".git"), Number: n}, nil
	}

	if m := slugPattern.FindStringSubmatch(ref); m != nil {
		n, err := strconv.Atoi(m[3])
		if err != nil {
			return Ref{}, fmt.Errorf("pull request %q: %w", ref, err)
		}
		return Ref{Owner: m[1], Repo: m[2], Number: n}, nil
	}

	// A bare number resolves against whichever repository dir belongs to.
	number, err := strconv.Atoi(strings.TrimPrefix(ref, "#"))
	if err != nil {
		return Ref{}, fmt.Errorf("cannot read %q as a pull request; use a number, owner/repo#number, or a URL", ref)
	}
	owner, repo, err := p.currentRepo(ctx, dir)
	if err != nil {
		return Ref{}, err
	}
	return Ref{Owner: owner, Repo: repo, Number: number}, nil
}

// currentRepo asks gh which repository the directory belongs to.
func (p *GitHubProvider) currentRepo(ctx context.Context, dir string) (owner, repo string, err error) {
	res, err := p.runner.Run(ctx, exec.Command{
		Name: p.binary,
		Args: []string{"repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"},
		Dir:  dir,
	})
	if err != nil {
		return "", "", fmt.Errorf("determine the current repository (pass owner/repo#number instead): %w", err)
	}

	slug := strings.TrimSpace(string(res.Stdout))
	owner, repo, ok := strings.Cut(slug, "/")
	if !ok || owner == "" || repo == "" {
		return "", "", fmt.Errorf("gh reported an unusable repository name %q", slug)
	}
	return owner, repo, nil
}

// ghPullRequest mirrors the subset of `gh pr view --json` peel consumes.
type ghPullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	URL         string `json:"url"`
	State       string `json:"state"`
	IsDraft     bool   `json:"isDraft"`
}

// prFields are the fields requested from `gh pr view`.
var prFields = strings.Join([]string{
	"number", "title", "body", "author",
	"baseRefName", "headRefName", "url", "state", "isDraft",
}, ",")

// Fetch implements Provider.
func (p *GitHubProvider) Fetch(ctx context.Context, ref Ref) (*PullRequest, error) {
	if !ref.Valid() {
		return nil, fmt.Errorf("incomplete pull request reference %+v", ref)
	}
	number := strconv.Itoa(ref.Number)

	res, err := p.runner.Run(ctx, exec.Command{
		Name: p.binary,
		Args: []string{"pr", "view", number, "--repo", ref.Slug(), "--json", prFields},
	})
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", ref, err)
	}

	var meta ghPullRequest
	if err := json.Unmarshal(res.Stdout, &meta); err != nil {
		return nil, fmt.Errorf("fetch %s: parse gh output: %w", ref, err)
	}

	diffRes, err := p.runner.Run(ctx, exec.Command{
		Name: p.binary,
		Args: []string{"pr", "diff", number, "--repo", ref.Slug()},
	})
	if err != nil {
		return nil, fmt.Errorf("fetch diff for %s: %w", ref, err)
	}

	return &PullRequest{
		Ref:     ref,
		Title:   meta.Title,
		Body:    meta.Body,
		Author:  meta.Author.Login,
		BaseRef: meta.BaseRefName,
		HeadRef: meta.HeadRefName,
		URL:     meta.URL,
		State:   meta.State,
		Draft:   meta.IsDraft,
		Diff:    string(diffRes.Stdout),
	}, nil
}

// reviewPayload is the request body for the create-review API.
type reviewPayload struct {
	Body     string                 `json:"body,omitempty"`
	Event    string                 `json:"event"`
	Comments []reviewCommentPayload `json:"comments,omitempty"`
}

type reviewCommentPayload struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side,omitempty"`
	Body string `json:"body"`
}

// SubmitReview implements Provider.
//
// This is the only operation in peel that writes to a service outside the
// user's machine. Callers confirm with the user first; nothing here does.
func (p *GitHubProvider) SubmitReview(ctx context.Context, ref Ref, review Review) error {
	if !ref.Valid() {
		return fmt.Errorf("incomplete pull request reference %+v", ref)
	}
	if err := review.Validate(); err != nil {
		return err
	}

	payload := reviewPayload{Body: review.Body, Event: string(review.Event)}
	for _, c := range review.Comments {
		side := c.Side
		if side == "" {
			side = "RIGHT"
		}
		payload.Comments = append(payload.Comments, reviewCommentPayload{
			Path: c.Path,
			Line: c.Line,
			Side: side,
			Body: c.Body,
		})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode review: %w", err)
	}

	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", ref.Owner, ref.Repo, ref.Number)
	_, err = p.runner.Run(ctx, exec.Command{
		Name:  p.binary,
		Args:  []string{"api", "--method", "POST", endpoint, "--input", "-"},
		Stdin: strings.NewReader(string(body)),
	})
	if err != nil {
		return fmt.Errorf("submit review to %s: %w", ref, err)
	}
	return nil
}
