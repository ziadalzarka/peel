// Command peel reviews a diff and stages what you just reviewed.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/ziadalzarka/peel/internal/app"
	"github.com/ziadalzarka/peel/internal/cli"
	"github.com/ziadalzarka/peel/internal/tui"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}

	c := &cli.CLI{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
		Dir:    dir,
		RunTUI: runTUI,
	}
	os.Exit(c.Run(ctx, os.Args[1:]))
}

// runTUI translates the CLI's display flags into UI options. It is the only
// place the two packages meet.
func runTUI(ctx context.Context, a *app.App, s *app.Session, ui cli.UIOptions) error {
	var opts []tui.Option
	if ui.Follow {
		opts = append(opts, tui.WithFollow(true))
	}
	if ui.Split {
		opts = append(opts, tui.WithLayout(tui.LayoutSplit))
	}
	return tui.Run(ctx, a, s, opts...)
}
