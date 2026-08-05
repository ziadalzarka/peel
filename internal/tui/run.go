package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/app"
)

// Run opens the review UI on a session.
func Run(ctx context.Context, a *app.App, s *app.Session, opts ...Option) error {
	cfg := options{}
	for _, opt := range opts {
		opt(&cfg)
	}
	backend := NewBackend(a, s, cfg.provider)
	comments, err := backend.Comments(ctx)
	if err != nil {
		return fmt.Errorf("load comments: %w", err)
	}

	model := New(ctx, backend, s, comments, opts...)
	// Mouse reporting is on so the wheel arrives as a wheel event. Without it
	// the terminal emulates arrow keys instead, which scroll by whatever the
	// arrows are bound to and in whichever direction the terminal decides.
	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	)
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("review UI: %w", err)
	}
	return nil
}
