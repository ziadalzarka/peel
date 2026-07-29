// Package registry holds interchangeable providers and picks between them.
//
// Both the AI and forge provider sets need the same behaviour — select by
// name, otherwise fall back to the first usable one — so they share this
// implementation rather than each growing their own.
package registry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNoneAvailable reports that nothing registered can run right now.
var ErrNoneAvailable = errors.New("no provider available")

// Provider is the minimum a registered implementation must expose.
type Provider interface {
	// Name is the stable identifier used to select it.
	Name() string
	// Description is one line explaining what it does, shown when listing.
	Description() string
	// Available reports whether it can run right now.
	Available(ctx context.Context) bool
}

// Registry holds providers of one kind in preference order.
type Registry[T Provider] struct {
	// kind names what is being registered, for error messages.
	kind      string
	providers []T
}

// New returns a registry of the given kind, in preference order.
func New[T Provider](kind string, providers ...T) *Registry[T] {
	r := &Registry[T]{kind: kind}
	for _, p := range providers {
		r.Register(p)
	}
	return r
}

// Register adds a provider. Registering a name that already exists replaces it,
// so a caller can override a built-in.
func (r *Registry[T]) Register(p T) {
	for i, existing := range r.providers {
		if existing.Name() == p.Name() {
			r.providers[i] = p
			return
		}
	}
	r.providers = append(r.providers, p)
}

// Names lists registered names in preference order.
func (r *Registry[T]) Names() []string {
	out := make([]string, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p.Name())
	}
	return out
}

// All returns every registered provider in preference order.
func (r *Registry[T]) All() []T {
	out := make([]T, len(r.providers))
	copy(out, r.providers)
	return out
}

// Get returns the provider with the given name, whether or not it is available.
func (r *Registry[T]) Get(name string) (T, error) {
	for _, p := range r.providers {
		if p.Name() == name {
			return p, nil
		}
	}
	var zero T
	known := r.Names()
	sort.Strings(known)
	if len(known) == 0 {
		return zero, fmt.Errorf("unknown %s %q; none are registered", r.kind, name)
	}
	return zero, fmt.Errorf("unknown %s %q; available: %s", r.kind, name, strings.Join(known, ", "))
}

// Resolve returns the named provider, or the first available one when name is
// empty. It fails rather than returning something unusable.
func (r *Registry[T]) Resolve(ctx context.Context, name string) (T, error) {
	var zero T

	if name != "" {
		p, err := r.Get(name)
		if err != nil {
			return zero, err
		}
		if !p.Available(ctx) {
			return zero, fmt.Errorf("%s %q is not available: %s", r.kind, name, p.Description())
		}
		return p, nil
	}

	for _, p := range r.providers {
		if p.Available(ctx) {
			return p, nil
		}
	}
	if len(r.providers) == 0 {
		return zero, fmt.Errorf("%w: no %s is registered", ErrNoneAvailable, r.kind)
	}
	return zero, fmt.Errorf("%w: tried %s", ErrNoneAvailable, strings.Join(r.Names(), ", "))
}
