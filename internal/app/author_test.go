package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/store"
)

type loginForge struct {
	*fakeForge
	login    string
	loginErr error
	asked    int
}

func (f *loginForge) Login(context.Context) (string, error) {
	f.asked++
	return f.login, f.loginErr
}

func newAuthorFixture(t *testing.T, login string, loginErr error) (*fixture, *loginForge) {
	t.Helper()
	gh := &loginForge{fakeForge: &fakeForge{name: "fake-forge", available: true}, login: login, loginErr: loginErr}
	f := newFixture(t, app.WithForgeRegistry(forge.NewRegistry(gh)))
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	return f, gh
}

func TestTheAuthorIsWhatGitConfigNames(t *testing.T) {
	f, gh := newAuthorFixture(t, "octocat", nil)
	f.repo.Git("config", "peel.author", "ziadalzarka")

	if got := f.app.Author(f.ctx); got != "ziadalzarka" {
		t.Errorf("Author = %q, want the name git config holds", got)
	}
	if gh.asked != 0 {
		t.Errorf("gh was asked for a login %d times with peel.author set", gh.asked)
	}
}

func TestTheAuthorFallsBackToTheForgeLoginAndKeepsIt(t *testing.T) {
	f, gh := newAuthorFixture(t, "octocat\n", nil)

	if got := f.app.Author(f.ctx); got != "octocat" {
		t.Fatalf("Author = %q, want the gh login", got)
	}
	if saved := f.repo.Git("config", "--global", "--get", "peel.author"); saved != "octocat" {
		t.Errorf("global peel.author = %q, want the login written down", saved)
	}
	if got := f.app.Author(f.ctx); got != "octocat" || gh.asked != 1 {
		t.Errorf("second Author = %q after %d logins, want octocat read back without asking again", got, gh.asked)
	}
}

func TestTheAuthorFallsBackToTheUserWithNoLogin(t *testing.T) {
	f, _ := newAuthorFixture(t, "", errors.New("gh: not logged in"))
	t.Setenv("USER", "someone")

	if got := f.app.Author(f.ctx); got != "someone" {
		t.Errorf("Author = %q, want $USER", got)
	}
	if saved, _ := f.repo.TryGit("config", "--global", "--get", "peel.author"); saved != "" {
		t.Errorf("global peel.author = %q, want nothing written for a name that is only a fallback", saved)
	}
}

func TestTheAuthorIsUserWithNothingElseToGoOn(t *testing.T) {
	f, _ := newAuthorFixture(t, "", errors.New("gh: not logged in"))
	t.Setenv("USER", "")

	if got := f.app.Author(f.ctx); got != store.AuthorUser {
		t.Errorf("Author = %q, want %q", got, store.AuthorUser)
	}
}
