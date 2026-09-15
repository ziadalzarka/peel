package app

import (
	"context"
	"fmt"

	"github.com/ziadalzarka/peel/internal/forge"
	"github.com/ziadalzarka/peel/internal/store"
)

func (a *App) importComments(ctx context.Context, provider forge.Provider, ref forge.Ref, target string) error {
	remote, err := provider.Comments(ctx, ref)
	if err != nil {
		return err
	}
	comments := make([]store.Comment, 0, len(remote))
	for _, r := range remote {
		side := store.SideNew
		if r.Side == "LEFT" {
			side = store.SideOld
		}
		comments = append(comments, store.Comment{
			ID:        provider.Name() + "-" + r.ID,
			File:      r.Path,
			Line:      r.Line,
			EndLine:   r.EndLine,
			Side:      side,
			Body:      r.Body,
			Author:    store.Author(r.Author),
			Resolved:  r.Resolved,
			CreatedAt: r.CreatedAt.UTC(),
			Remote:    provider.Name(),
		})
	}
	if err := store.NewReviewStore(a.ReviewPath(target), target, a.storeOpts...).Import(comments); err != nil {
		return fmt.Errorf("keep the review comments on %s: %w", ref, err)
	}
	return nil
}
