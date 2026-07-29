// Package ai generates written narratives of a changeset.
//
// Providers are pluggable: peel ships one that shells out to the Claude Code
// CLI, and adding another means implementing Provider and registering it. No
// other package refers to a concrete provider.
package ai

import (
	"context"

	"github.com/ziadalzarka/peel/internal/registry"
)

// ErrNoProvider reports that no registered provider is usable right now.
var ErrNoProvider = registry.ErrNoneAvailable

// Request is the changeset to summarise.
type Request struct {
	// Diff is the unified diff to describe.
	Diff string
	// Target names what is being reviewed, for the narrative's framing —
	// "the working tree" or "pull request #412".
	Target string
	// Files lists the paths involved, so a provider can mention scope without
	// re-parsing the diff.
	Files []string
	// Instruction optionally replaces the default task description.
	Instruction string
}

// Provider turns a diff into prose.
type Provider interface {
	registry.Provider

	// Walkthrough returns a markdown narrative of the request.
	Walkthrough(ctx context.Context, req Request) (string, error)
}

// Registry holds the known AI providers and picks between them.
type Registry = registry.Registry[Provider]

// NewRegistry returns a registry containing the given providers, in preference
// order.
func NewRegistry(providers ...Provider) *Registry {
	return registry.New("AI provider", providers...)
}
