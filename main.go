// Command peel reviews a diff and stages what you just reviewed.
package main

import (
	"context"
	"os"
	"os/signal"

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
		RunTUI: tui.Run,
	}
	os.Exit(c.Run(ctx, os.Args[1:]))
}
