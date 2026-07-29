package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ziadalzarka/peel/internal/app"
)

// Run opens the review UI on a session. It matches cli.TUIRunner.
func Run(ctx context.Context, a *app.App, s *app.Session) error {
	backend := NewBackend(a, s)
	comments, err := backend.Comments()
	if err != nil {
		return fmt.Errorf("load comments: %w", err)
	}

	model := New(ctx, backend, s, comments)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("review UI: %w", err)
	}
	return nil
}
