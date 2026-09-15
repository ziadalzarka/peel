package app

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/ziadalzarka/peel/internal/store"
)

const AuthorKey = ConfigSection + ".author"

const loginTimeout = 5 * time.Second

type loginLookup interface {
	Login(ctx context.Context) (string, error)
}

func (a *App) Author(ctx context.Context) store.Author {
	if cfg, err := a.Repo.ConfigSection(ctx, ConfigSection); err == nil {
		if name := store.Author(strings.TrimSpace(cfg[AuthorKey])); name.Valid() {
			return name
		}
	}
	if login := a.forgeLogin(ctx); login.Valid() {
		_ = a.Repo.SetGlobalConfig(ctx, AuthorKey, string(login))
		return login
	}
	if name := store.Author(strings.TrimSpace(os.Getenv("USER"))); name.Valid() {
		return name
	}
	return store.AuthorUser
}

func (a *App) forgeLogin(ctx context.Context) store.Author {
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	for _, provider := range a.Forges.All() {
		lookup, ok := provider.(loginLookup)
		if !ok || !provider.Available(ctx) {
			continue
		}
		if login, err := lookup.Login(ctx); err == nil {
			return store.Author(strings.TrimSpace(login))
		}
	}
	return ""
}
