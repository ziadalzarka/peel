package app_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ziadalzarka/peel/internal/ai"
	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/exec"
	"github.com/ziadalzarka/peel/internal/gittest"
)

func TestDefaultAIProvidersPreferClaudeThenCodex(t *testing.T) {
	repo := gittest.New(t)
	runner := exec.NewFakeRunner().
		Respond("rev-parse --show-toplevel", repo.Dir+"\n").
		Respond("rev-parse --absolute-git-dir", filepath.Join(repo.Dir, ".git")+"\n")

	a, err := app.Open(context.Background(), repo.Dir, app.WithRunner(runner))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got := a.AI.Names()
	if len(got) != 2 || got[0] != "claude-code" || got[1] != "codex" {
		t.Errorf("AI providers = %v, want [claude-code codex]", got)
	}
}

func TestWalkthroughSwitchesAwayFromCachedProvider(t *testing.T) {
	claude := &fakeAI{name: "claude-code", available: true, body: "from Claude"}
	codex := &fakeAI{name: "codex", available: true, body: "from Codex"}
	f := newFixture(t, app.WithAIRegistry(ai.NewRegistry(claude, codex)))
	f.repo.Write("a.txt", "one\n")
	f.repo.Commit("base")
	f.repo.Write("a.txt", "two\n")

	s, err := f.app.LoadWorkingTree(f.ctx)
	if err != nil {
		t.Fatalf("LoadWorkingTree: %v", err)
	}
	if _, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{}); err != nil {
		t.Fatalf("default Walkthrough: %v", err)
	}

	got, err := f.app.Walkthrough(f.ctx, s, app.WalkthroughRequest{Provider: "codex"})
	if err != nil {
		t.Fatalf("Codex Walkthrough: %v", err)
	}
	if got.Provider != "codex" || got.Body != "from Codex" {
		t.Errorf("Walkthrough = %+v, want a Codex result", got)
	}
	if len(claude.requests) != 1 || len(codex.requests) != 1 {
		t.Errorf("provider calls: Claude %d, Codex %d; want one each", len(claude.requests), len(codex.requests))
	}
}
