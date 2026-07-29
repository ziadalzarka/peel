package registry

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stub is a minimal Provider whose availability tests control.
type stub struct {
	name      string
	available bool
}

func (s *stub) Name() string                   { return s.name }
func (s *stub) Description() string            { return "stub called " + s.name }
func (s *stub) Available(context.Context) bool { return s.available }

func newTestRegistry(providers ...*stub) *Registry[*stub] {
	return New("test provider", providers...)
}

func TestResolveFirstAvailable(t *testing.T) {
	missing := &stub{name: "missing", available: false}
	present := &stub{name: "present", available: true}
	later := &stub{name: "later", available: true}

	got, err := newTestRegistry(missing, present, later).Resolve(context.Background(), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Name() != "present" {
		t.Errorf("Resolve() = %q, want the first available provider", got.Name())
	}
}

func TestResolveByName(t *testing.T) {
	r := newTestRegistry(&stub{name: "a", available: true}, &stub{name: "b", available: true})

	got, err := r.Resolve(context.Background(), "b")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Name() != "b" {
		t.Errorf("Resolve(b) = %q", got.Name())
	}
}

func TestResolveNamedButUnavailable(t *testing.T) {
	r := newTestRegistry(&stub{name: "a", available: false})

	_, err := r.Resolve(context.Background(), "a")
	if err == nil {
		t.Fatal("Resolve succeeded for an unavailable provider")
	}
	// The message must include the description, which explains what is missing.
	if !strings.Contains(err.Error(), "not available") || !strings.Contains(err.Error(), "stub called a") {
		t.Errorf("error = %v, want it to explain why", err)
	}
}

func TestResolveUnknownNameListsAlternatives(t *testing.T) {
	r := newTestRegistry(&stub{name: "github"}, &stub{name: "gitlab"})

	_, err := r.Resolve(context.Background(), "bitbucket")
	if err == nil {
		t.Fatal("Resolve succeeded for an unknown name")
	}
	for _, want := range []string{"test provider", "github", "gitlab"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", err, want)
		}
	}
}

func TestResolveNoneAvailable(t *testing.T) {
	r := newTestRegistry(&stub{name: "a", available: false}, &stub{name: "b", available: false})

	_, err := r.Resolve(context.Background(), "")
	if !errors.Is(err, ErrNoneAvailable) {
		t.Fatalf("error = %v, want ErrNoneAvailable", err)
	}
	// Naming what was tried is the difference between a usable error and not.
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Errorf("error = %v, want it to list what was tried", err)
	}
}

func TestResolveEmptyRegistry(t *testing.T) {
	_, err := newTestRegistry().Resolve(context.Background(), "")
	if !errors.Is(err, ErrNoneAvailable) {
		t.Errorf("error = %v, want ErrNoneAvailable", err)
	}
}

func TestGetIgnoresAvailability(t *testing.T) {
	// Get is for inspection, so it must return providers that cannot run.
	r := newTestRegistry(&stub{name: "a", available: false})

	got, err := r.Get("a")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name() != "a" {
		t.Errorf("Get(a) = %q", got.Name())
	}
}

func TestGetUnknownOnEmptyRegistry(t *testing.T) {
	_, err := newTestRegistry().Get("a")
	if err == nil {
		t.Fatal("Get succeeded on an empty registry")
	}
	if !strings.Contains(err.Error(), "none are registered") {
		t.Errorf("error = %v, want it to say nothing is registered", err)
	}
}

func TestRegisterReplacesSameName(t *testing.T) {
	original := &stub{name: "claude-code", available: false}
	override := &stub{name: "claude-code", available: true}

	r := newTestRegistry(original)
	r.Register(override)

	if names := r.Names(); len(names) != 1 {
		t.Fatalf("Names() = %v, want a single entry after replacement", names)
	}
	got, err := r.Resolve(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != override {
		t.Error("Resolve returned the replaced provider")
	}
}

func TestRegisterPreservesPositionOnReplace(t *testing.T) {
	r := newTestRegistry(&stub{name: "a"}, &stub{name: "b"}, &stub{name: "c"})
	r.Register(&stub{name: "b", available: true})

	want := []string{"a", "b", "c"}
	got := r.Names()
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names() = %v, want %v — replacement reordered the registry", got, want)
		}
	}
}

func TestNamesAndAllPreserveOrder(t *testing.T) {
	r := newTestRegistry(&stub{name: "first"}, &stub{name: "second"}, &stub{name: "third"})

	want := []string{"first", "second", "third"}
	for i, name := range r.Names() {
		if name != want[i] {
			t.Errorf("Names()[%d] = %q, want %q", i, name, want[i])
		}
	}
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d providers, want 3", len(all))
	}
	for i, p := range all {
		if p.Name() != want[i] {
			t.Errorf("All()[%d] = %q, want %q", i, p.Name(), want[i])
		}
	}
}

func TestAllReturnsACopy(t *testing.T) {
	r := newTestRegistry(&stub{name: "a"}, &stub{name: "b"})

	all := r.All()
	all[0] = &stub{name: "mutated"}

	if r.Names()[0] != "a" {
		t.Error("mutating the slice from All() changed the registry")
	}
}
