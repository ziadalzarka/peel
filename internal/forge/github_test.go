package forge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ziadalzarka/peel/internal/exec"
)

func newGitHub(runner exec.Runner) *GitHubProvider {
	return NewGitHub(runner, WithGHLookPath(func(string) bool { return true }))
}

func TestParseReferenceForms(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Ref
	}{
		{"url", "https://github.com/cli/cli/pull/412", Ref{"cli", "cli", 412}},
		{"url with trailing path", "https://github.com/cli/cli/pull/412/files", Ref{"cli", "cli", 412}},
		{"enterprise url", "https://git.corp.example/team/svc/pull/7", Ref{"team", "svc", 7}},
		{"http url", "http://github.com/o/r/pull/9", Ref{"o", "r", 9}},
		{"slug with hash", "cli/cli#412", Ref{"cli", "cli", 412}},
		{"slug with slash", "cli/cli/412", Ref{"cli", "cli", 412}},
	}

	p := newGitHub(exec.NewFakeRunner())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.Parse(context.Background(), "", tt.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseBareNumberUsesCurrentRepo(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("gh repo view", "ziadalzarka/peel\n")
	p := newGitHub(runner)

	got, err := p.Parse(context.Background(), "/repo", "412")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if want := (Ref{"ziadalzarka", "peel", 412}); got != want {
		t.Errorf("Parse(412) = %+v, want %+v", got, want)
	}
	// The lookup must run in the repository being reviewed, not the cwd.
	if dir := runner.Calls()[0].Cmd.Dir; dir != "/repo" {
		t.Errorf("gh ran in %q, want /repo", dir)
	}
}

func TestParseBareNumberWithLeadingHash(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("gh repo view", "o/r\n")
	got, err := newGitHub(runner).Parse(context.Background(), "", "#7")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Number != 7 {
		t.Errorf("Number = %d, want 7", got.Number)
	}
}

func TestParseRejectsNonsense(t *testing.T) {
	p := newGitHub(exec.NewFakeRunner())
	for _, in := range []string{"", "   ", "not-a-pr", "owner/repo"} {
		t.Run(in, func(t *testing.T) {
			if _, err := p.Parse(context.Background(), "", in); err == nil {
				t.Fatalf("Parse(%q) succeeded, want an error", in)
			}
		})
	}
}

func TestParseSuggestsFallbackWhenRepoUnknown(t *testing.T) {
	runner := exec.NewFakeRunner().RespondErr("gh repo view", "no git remote found", 1)

	_, err := newGitHub(runner).Parse(context.Background(), "", "412")
	if err == nil {
		t.Fatal("Parse succeeded with no resolvable repository")
	}
	if !strings.Contains(err.Error(), "owner/repo#number") {
		t.Errorf("error = %v, want it to suggest the explicit form", err)
	}
}

const prJSON = `{
  "number": 412,
  "title": "Drop the document key",
  "body": "Removes the key from the model.",
  "author": {"login": "ziadalzarka"},
  "baseRefName": "main",
  "headRefName": "feature/drop-key",
  "url": "https://github.com/cli/cli/pull/412",
  "state": "OPEN",
  "isDraft": true
}`

func TestFetch(t *testing.T) {
	const diff = "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n@@ -1 +1 @@\n-a\n+b\n"
	runner := exec.NewFakeRunner().
		Respond("gh pr view 412 --repo cli/cli --json", prJSON).
		Respond("gh pr diff 412 --repo cli/cli", diff)

	got, err := newGitHub(runner).Fetch(context.Background(), Ref{"cli", "cli", 412})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if got.Title != "Drop the document key" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Author != "ziadalzarka" {
		t.Errorf("Author = %q", got.Author)
	}
	if got.BaseRef != "main" || got.HeadRef != "feature/drop-key" {
		t.Errorf("refs = %q..%q", got.BaseRef, got.HeadRef)
	}
	if !got.Draft {
		t.Error("Draft = false, want true")
	}
	if got.Diff != diff {
		t.Errorf("Diff = %q", got.Diff)
	}
}

func TestFetchRejectsIncompleteRef(t *testing.T) {
	p := newGitHub(exec.NewFakeRunner())
	for _, ref := range []Ref{{}, {Owner: "o"}, {Owner: "o", Repo: "r"}} {
		if _, err := p.Fetch(context.Background(), ref); err == nil {
			t.Errorf("Fetch(%+v) succeeded, want an error", ref)
		}
	}
}

func TestFetchSurfacesGHError(t *testing.T) {
	runner := exec.NewFakeRunner().RespondErr("gh pr view", "could not resolve to a PullRequest", 1)

	_, err := newGitHub(runner).Fetch(context.Background(), Ref{"cli", "cli", 999})
	if err == nil {
		t.Fatal("Fetch succeeded despite a gh failure")
	}
	if !strings.Contains(err.Error(), "could not resolve") {
		t.Errorf("error = %v, want gh's stderr surfaced", err)
	}
}

func TestFetchRejectsMalformedJSON(t *testing.T) {
	runner := exec.NewFakeRunner().
		Respond("gh pr view", "{not json").
		Respond("gh pr diff", "")

	if _, err := newGitHub(runner).Fetch(context.Background(), Ref{"o", "r", 1}); err == nil {
		t.Fatal("Fetch accepted malformed JSON")
	}
}

func TestSubmitReviewPayload(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("gh api", "{}")

	review := Review{
		Body:  "A couple of things",
		Event: EventRequestChanges,
		Comments: []ReviewComment{
			{Path: "src/main.go", Line: 42, Body: "this leaks the tx"},
			{Path: "src/auth.go", Line: 7, Side: "LEFT", Body: "why was this removed?"},
		},
	}
	if err := newGitHub(runner).SubmitReview(context.Background(), Ref{"cli", "cli", 412}, review); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}

	call := runner.Calls()[0]
	if got := call.Cmd.String(); !strings.Contains(got, "repos/cli/cli/pulls/412/reviews") {
		t.Errorf("endpoint = %q", got)
	}
	if !strings.Contains(call.Cmd.String(), "--method POST") {
		t.Errorf("command = %q, want a POST", call.Cmd.String())
	}

	var payload reviewPayload
	if err := json.Unmarshal([]byte(call.Stdin), &payload); err != nil {
		t.Fatalf("payload is not valid JSON: %v\n%s", err, call.Stdin)
	}
	if payload.Event != "REQUEST_CHANGES" {
		t.Errorf("Event = %q", payload.Event)
	}
	if payload.Body != "A couple of things" {
		t.Errorf("Body = %q", payload.Body)
	}
	if len(payload.Comments) != 2 {
		t.Fatalf("got %d comments, want 2", len(payload.Comments))
	}
	// An unset side must default to the changed file, not be omitted.
	if payload.Comments[0].Side != "RIGHT" {
		t.Errorf("comment 0 Side = %q, want RIGHT by default", payload.Comments[0].Side)
	}
	if payload.Comments[1].Side != "LEFT" {
		t.Errorf("comment 1 Side = %q, want LEFT preserved", payload.Comments[1].Side)
	}
}

func TestSubmitReviewBodyOnly(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("gh api", "{}")

	err := newGitHub(runner).SubmitReview(context.Background(), Ref{"o", "r", 1},
		Review{Body: "looks good", Event: EventApprove})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}

	var payload reviewPayload
	if err := json.Unmarshal([]byte(runner.LastStdin()), &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if len(payload.Comments) != 0 {
		t.Errorf("Comments = %v, want none", payload.Comments)
	}
}

func TestSubmitReviewValidation(t *testing.T) {
	tests := []struct {
		name   string
		review Review
	}{
		{"unknown event", Review{Body: "x", Event: "MAYBE"}},
		{"empty review", Review{Event: EventComment}},
		{"comment with no path", Review{Event: EventComment, Comments: []ReviewComment{{Line: 1, Body: "x"}}}},
		{"comment with no body", Review{Event: EventComment, Comments: []ReviewComment{{Path: "f.go", Line: 1}}}},
		{"comment with no line", Review{Event: EventComment, Comments: []ReviewComment{{Path: "f.go", Body: "x"}}}},
		{"comment with bad side", Review{Event: EventComment, Comments: []ReviewComment{{Path: "f.go", Line: 1, Body: "x", Side: "MIDDLE"}}}},
	}

	// A failed validation must not reach the network.
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := exec.NewFakeRunner()
			err := newGitHub(runner).SubmitReview(context.Background(), Ref{"o", "r", 1}, tt.review)
			if err == nil {
				t.Fatal("SubmitReview succeeded, want a validation error")
			}
			if len(runner.Calls()) != 0 {
				t.Errorf("an invalid review still called gh: %v", runner.Calls())
			}
		})
	}
}

func TestSubmitReviewSurfacesGHError(t *testing.T) {
	runner := exec.NewFakeRunner().RespondErr("gh api", "HTTP 422: line must be part of the diff", 1)

	err := newGitHub(runner).SubmitReview(context.Background(), Ref{"o", "r", 1},
		Review{Body: "x", Event: EventComment})
	if err == nil {
		t.Fatal("SubmitReview succeeded despite an API failure")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error = %v, want the API error surfaced", err)
	}
}

func TestAvailability(t *testing.T) {
	present := NewGitHub(exec.NewFakeRunner(), WithGHLookPath(func(string) bool { return true }))
	if !present.Available(context.Background()) {
		t.Error("Available() = false with gh on PATH")
	}
	absent := NewGitHub(exec.NewFakeRunner(), WithGHLookPath(func(string) bool { return false }))
	if absent.Available(context.Background()) {
		t.Error("Available() = true without gh")
	}
}

func TestCustomBinary(t *testing.T) {
	runner := exec.NewFakeRunner().Respond("gh-next repo view", "o/r\n")
	p := NewGitHub(runner,
		WithGHBinary("gh-next"),
		WithGHLookPath(func(string) bool { return true }),
	)

	if _, err := p.Parse(context.Background(), "", "1"); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := runner.Calls()[0].Cmd.Name; got != "gh-next" {
		t.Errorf("binary = %q", got)
	}
}

func TestRefRendering(t *testing.T) {
	r := Ref{Owner: "cli", Repo: "cli", Number: 412}
	if got := r.Slug(); got != "cli/cli" {
		t.Errorf("Slug() = %q", got)
	}
	if got := r.String(); got != "cli/cli#412" {
		t.Errorf("String() = %q", got)
	}
	if got := r.Target("github"); got != "github:cli/cli#412" {
		t.Errorf("Target() = %q", got)
	}
	if !r.Valid() {
		t.Error("Valid() = false for a complete ref")
	}
	if (Ref{Owner: "cli", Repo: "cli"}).Valid() {
		t.Error("Valid() = true for a ref with no number")
	}
}

func TestPullRequestDescribe(t *testing.T) {
	tests := []struct {
		name string
		pr   PullRequest
		want string
	}{
		{
			name: "full",
			pr:   PullRequest{Ref: Ref{Number: 412}, Title: "Drop key", Author: "ziad", Draft: true},
			want: "#412 Drop key · ziad · draft",
		},
		{
			name: "no author",
			pr:   PullRequest{Ref: Ref{Number: 7}, Title: "Fix"},
			want: "#7 Fix",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pr.Describe(); got != tt.want {
				t.Errorf("Describe() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReviewEventValid(t *testing.T) {
	for _, e := range []ReviewEvent{EventComment, EventApprove, EventRequestChanges} {
		if !e.Valid() {
			t.Errorf("%q.Valid() = false", e)
		}
	}
	if ReviewEvent("MERGE").Valid() {
		t.Error("MERGE reported as a valid review event")
	}
}

var _ Provider = (*GitHubProvider)(nil)
