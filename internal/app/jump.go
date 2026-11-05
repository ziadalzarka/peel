package app

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"github.com/ziadalzarka/peel/internal/store"
)

const (
	jumpFileName = "jump.json"
	jumpSuffix   = ".jump"
)

func (a *App) PullRequestTarget(ctx context.Context, providerName, ref string) (string, error) {
	provider, err := a.Forges.Resolve(ctx, providerName)
	if err != nil {
		return "", err
	}
	parsed, err := provider.Parse(ctx, a.Root, ref)
	if err != nil {
		return "", err
	}
	return parsed.Target(provider.Name()), nil
}

func (a *App) AskToJump(target, file string, line int) error {
	path, err := a.jumpPath(target)
	if err != nil {
		return err
	}
	return store.WriteJump(path, store.Jump{File: file, Line: line, At: time.Now().UTC()})
}

func (a *App) PendingJump(target string) (store.Jump, error) {
	path, err := a.jumpPath(target)
	if err != nil {
		return store.Jump{}, err
	}
	return store.ReadJump(path)
}

func (a *App) jumpPath(target string) (string, error) {
	if target != "" {
		return strings.TrimSuffix(a.ReviewPath(target), ".json") + jumpSuffix, nil
	}
	if err := a.needRepo(); err != nil {
		return "", err
	}
	return filepath.Join(a.StateDir, jumpFileName), nil
}
