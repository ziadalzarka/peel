package tui_test

import (
	"context"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/tui"
)

type walkthroughProvider struct {
	name  string
	body  string
	calls int
}

func (p *walkthroughProvider) Name() string        { return p.name }
func (p *walkthroughProvider) Description() string { return p.name }
func (p *walkthroughProvider) Available(context.Context) bool {
	return true
}
func (p *walkthroughProvider) Walkthrough(context.Context, ai.Request) (string, error) {
	p.calls++
	return p.body, nil
}

func TestBackendUsesSelectedWalkthroughProvider(t *testing.T) {
	a, session, _ := openBackend(t)
	claude := &walkthroughProvider{name: "claude-code", body: "from Claude"}
	codex := &walkthroughProvider{name: "codex", body: "from Codex"}
	a.AI = ai.NewRegistry(claude, codex)

	backend := tui.NewBackend(a, session, "codex")
	got, err := backend.Walkthrough(context.Background(), false)
	if err != nil {
		t.Fatalf("Walkthrough: %v", err)
	}
	if got != "from Codex" {
		t.Errorf("Walkthrough = %q, want Codex output", got)
	}
	if claude.calls != 0 || codex.calls != 1 {
		t.Errorf("provider calls: Claude %d, Codex %d; want 0 and 1", claude.calls, codex.calls)
	}
}
