package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/store"
)

// State is where one review is kept: what was written on it, what has been
// folded away, how it was being looked at, and the narrative generated for it.
//
// Which files these are depends on what is being reviewed, not on where peel
// was started. The working tree's review is this checkout's — it means nothing
// in another clone — so it stays in .git/peel. A pull request's is not: #412 is
// the same pull request from every worktree, from another clone, and from a
// directory that is not a repository at all, so its review is filed under the
// user's own state directory by the pull request it is about.
type State struct {
	Comments     store.CommentStore
	Folds        store.FoldStore
	Views        store.ViewStore
	Walkthroughs store.WalkthroughCache
}

// StateFor returns the state of what s is reviewing. A nil session, or one with
// no pull request behind it, is this repository's own.
func (a *App) StateFor(s *Session) State {
	if s == nil || s.PR == nil {
		return a.Local
	}
	return a.StateForTarget(s.Target)
}

// StateForTarget is the same thing named by the target alone, for the callers
// holding a comment rather than the session it was written in.
func (a *App) StateForTarget(target string) State {
	if target == "" {
		return a.Local
	}
	return a.reviewState(target)
}

// reviewState opens the file holding one review, wherever that review is read
// from.
func (a *App) reviewState(target string) State {
	review := store.NewReviewStore(a.ReviewPath(target), target, a.storeOpts...)
	return State{
		Comments:     review.Comments(),
		Folds:        review.Folds(),
		Views:        review.Views(),
		Walkthroughs: review.Walkthroughs(),
	}
}

// ReviewsDirName is the directory under the state directory holding one file
// per review that is not a working tree's.
const ReviewsDirName = "reviews"

// ReviewPath is the file a review's state lives in, named after the review
// itself: `reviews/github/cli/cli/412.json` under the state directory.
//
// A target peel cannot read as a pull request — nothing does that today — is
// filed under a name made from the target itself rather than dropped, so a
// provider added later still keeps its state somewhere predictable.
func (a *App) ReviewPath(target string) string {
	dir := filepath.Join(a.GlobalDir, ReviewsDirName)
	provider, ref, ok := forge.ParseTarget(target)
	if !ok {
		return filepath.Join(dir, pathSafe(target)+".json")
	}
	return filepath.Join(dir,
		pathSafe(provider), pathSafe(ref.Owner), pathSafe(ref.Repo),
		fmt.Sprintf("%d.json", ref.Number))
}

// pathSafe turns one part of a target into one path component, so nothing a
// code host can put in an owner or a repository name reaches outside the
// reviews directory.
func pathSafe(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case strings.ContainsRune("-_.", r):
			return r
		default:
			return '-'
		}
	}, s)
	// A component of dots alone is a way up the tree, not a name.
	if strings.Trim(out, ".") == "" {
		return "review"
	}
	return out
}

// GlobalStateDir is where peel keeps what is not one checkout's: $PEEL_STATE_DIR
// if it is set, else the XDG state directory, else ~/.local/state/peel.
//
// It is state rather than cache — these are review notes, not something peel can
// regenerate — so it deliberately does not go where the release check's cache
// goes, and the same path is used on every platform so a review is findable
// without knowing which one wrote it.
func GlobalStateDir() (string, error) {
	if dir := os.Getenv("PEEL_STATE_DIR"); dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, StateDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find the home directory for peel's state: %w", err)
	}
	return filepath.Join(home, ".local", "state", StateDirName), nil
}

// adoptLocalReview moves a pull request's notes out of the repository they were
// written in and into the file keyed by the pull request itself.
//
// Notes written before reviews were filed this way are in some checkout's
// .git/peel, which is exactly the place they cannot be read back from anywhere
// else. Opening the pull request from that checkout is the one moment peel can
// see both, so that is where they are moved — once, since afterwards there is
// nothing left locally to move.
func (a *App) adoptLocalReview(target string) error {
	if !a.HasRepo() || target == "" {
		return nil
	}
	local, err := a.Local.Comments.List(store.Filter{Target: target, MatchTarget: true})
	if err != nil || len(local) == 0 {
		// A local store that cannot be read holds nothing worth failing the
		// review over: what it would have carried is notes, and the review opens
		// with the ones already filed under the pull request.
		return nil
	}

	comments := a.reviewState(target).Comments
	for _, c := range local {
		if _, err := comments.Add(c); err != nil {
			return fmt.Errorf("move %s into this pull request's own review: %w", c.Location(), err)
		}
		if err := a.Local.Comments.Remove(c.ID); err != nil {
			return fmt.Errorf("take %s out of %s: %w", c.Location(), a.StateDir, err)
		}
	}
	return nil
}
