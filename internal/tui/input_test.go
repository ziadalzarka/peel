package tui

import (
	"bytes"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// runBytes drives a real bubbletea program with the bytes a terminal would send,
// and returns the model it finished on.
//
// The keys the modifier arrows rely on are ones bubbletea has no name for, and
// peel reads them off a message type bubbletea does not export. Feeding a
// stand-in message into Update cannot check that: what has to hold is that the
// program's own input reader turns these bytes into the message peel is looking
// for. So this runs the loop bubbletea would run over a terminal, on a pipe.
func runBytes(t *testing.T, m *Model, seq string) *Model {
	t.Helper()
	// ctrl+c closes the program, so Run returns rather than waiting on a terminal
	// that is never going to send anything else. It is the quit key rather than
	// `q` because bubbletea reads printable runes arriving together as one press,
	// and a `q` behind `]` would be read as the one key `]q` and quit nothing.
	in := bytes.NewReader(append([]byte(seq), 0x03))
	done := make(chan tea.Model, 1)
	go func() {
		final, err := tea.NewProgram(m,
			tea.WithInput(in),
			tea.WithOutput(io.Discard),
			tea.WithoutRenderer(),
		).Run()
		if err != nil {
			t.Errorf("Run: %v", err)
		}
		done <- final
	}()

	select {
	case final := <-done:
		model, ok := final.(*Model)
		if !ok {
			t.Fatalf("the program finished on a %T, want a *Model", final)
		}
		return model
	case <-time.After(10 * time.Second):
		t.Fatalf("the program did not finish after %q", seq)
		return nil
	}
}

// The bytes a terminal actually sends for the modifier arrows reach the cursor,
// read through bubbletea's own input loop rather than handed to Update as a
// message a test made up.
func TestTerminalBytesMoveTheCursor(t *testing.T) {
	tests := []struct {
		name string
		seq  string
		want func(d Document) int
	}{
		{
			// Ghostty and kitty, and what `keybind = super+arrow_down=csi:1;9B`
			// sends. bubbletea has no key for it and passes the bytes on.
			name: "cmd+↓",
			seq:  "\x1b[1;9B",
			want: func(d Document) int { return d.LastStop() },
		},
		{
			name: "cmd+↑ from the bottom",
			seq:  "\x1b[1;9B\x1b[1;9A",
			want: func(d Document) int { return d.FirstStop() },
		},
		{
			// An option-modified arrow, which bubbletea does name. It moves a whole
			// file, so it lands on the second file's header.
			name: "opt+↓",
			seq:  "\x1b[1;3B",
			want: func(d Document) int { return d.RowOfFile(1) },
		},
		{
			// The same press from a terminal that sends option as a leading escape
			// instead of as a modifier parameter.
			name: "esc-prefixed ↓",
			seq:  "\x1b\x1b[B",
			want: func(d Document) int { return d.RowOfFile(1) },
		},
		{
			// The brackets are the ten-line jump, which is at most ten presses of
			// the arrow and stops short where something is in the way.
			name: "]",
			seq:  "]",
			want: func(d Document) int { return d.Leap(d.FirstStop(), 10) },
		},
		{
			// A plain arrow still moves one line, so reading the modified forms has
			// not swallowed the unmodified one.
			name: "↓",
			seq:  "\x1b[B",
			want: func(d Document) int { return d.NextStop(d.FirstStop()) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(t, newFakeBackend(newSession(t, threeFileDiff)), WithSize(100, 12))
			start := m.cursor
			final := runBytes(t, m, tc.seq)

			want := tc.want(final.doc)
			if final.cursor != want {
				t.Errorf("%q left the cursor at row %d, want %d (it started at %d)",
					tc.seq, final.cursor, want, start)
			}
		})
	}
}
