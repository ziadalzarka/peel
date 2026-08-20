package app

import (
	"context"
	"fmt"
	"strings"
)

// Move names where the cursor goes once a file has been dealt with.
//
// Dealing with a file is two keys — `s` puts it in the index, `space` only puts
// it away — and they are set apart because they are not always the same pass. A
// review that stages is walking the index down and wants the next file that is
// not in it; a review that cannot stage has only the fold to go on.
type Move string

const (
	// MoveUnstaged carries the pass on to the next file below with anything
	// still out of the index, folded or not. What is left to stage is what is
	// left to do, and a fold is only how a file got off the screen.
	//
	// A session that cannot be staged has no index to read this against, so
	// there it means the next file still open.
	MoveUnstaged Move = "next-unstaged"
	// MoveOpen carries it on to the next file below still open, whatever the
	// index holds: the fold is the record of the pass and being staged says
	// nothing about having been read.
	MoveOpen Move = "next-open"
	// MoveStay leaves the cursor on the file just dealt with.
	MoveStay Move = "stay"
)

// Moves is where the cursor goes after each of the two keys that finish with a
// file.
type Moves struct {
	AfterStage Move
	AfterFold  Move
}

// AfterStageKey and AfterFoldKey are the git config settings behind them. Git
// stores the last component of a key lower-cased, so `peel.afterStage` is what
// is written and this is what comes back.
const (
	AfterStageKey = ConfigSection + ".afterstage"
	AfterFoldKey  = ConfigSection + ".afterfold"
)

// DefaultMoves is what peel does with neither setting written.
func DefaultMoves() Moves {
	return Moves{AfterStage: MoveUnstaged, AfterFold: MoveUnstaged}
}

// OrDefault fills in whichever of the two was left unset, so a caller holding
// half a setting does not get a cursor that goes nowhere.
func (m Moves) OrDefault() Moves {
	d := DefaultMoves()
	if m.AfterStage == "" {
		m.AfterStage = d.AfterStage
	}
	if m.AfterFold == "" {
		m.AfterFold = d.AfterFold
	}
	return m
}

// moveValues names what a move setting takes, for the error a typo gets.
const moveValues = `"next-unstaged", "next-open" or "stay"`

// ParseMove reads one setting's value.
func ParseMove(s string) (Move, error) {
	switch m := Move(strings.ToLower(strings.TrimSpace(s))); m {
	case MoveUnstaged, MoveOpen, MoveStay:
		return m, nil
	}
	return "", fmt.Errorf("%q is not %s", s, moveValues)
}

// Moves reads both settings from git config, most specific file last, the way
// every other peel setting is read.
//
// A value peel does not understand is worth saying and not worth refusing a
// review over: that setting keeps its default, the reviewer is told which key
// was not read, and the pass goes on. Both are read once at startup rather than
// on every keypress, since staging draws before it asks git anything and a
// config read there would be the one thing the keypress waited for.
func (a *App) Moves(ctx context.Context) (Moves, error) {
	moves := DefaultMoves()
	cfg, err := a.Repo.ConfigSection(ctx, ConfigSection)
	if err != nil {
		return moves, err
	}

	var bad []string
	for _, setting := range []struct {
		key string
		to  *Move
	}{{AfterStageKey, &moves.AfterStage}, {AfterFoldKey, &moves.AfterFold}} {
		raw := strings.TrimSpace(cfg[setting.key])
		if raw == "" {
			continue
		}
		move, err := ParseMove(raw)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", setting.key, err))
			continue
		}
		*setting.to = move
	}
	if len(bad) > 0 {
		return moves, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return moves, nil
}
