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

// StageMode is what `s` takes: the whole file the cursor is in, or the one hunk
// it is in.
//
// It is a mode rather than a key each because it is one decision about the pass
// rather than a decision about each file — a review that reads whole files
// stages whole files, and a review that goes hunk by hunk goes hunk by hunk, and
// either way the key under the finger is the same one. `S` switches it where a
// file wants the other size, so a pass does not have to be declared in advance,
// and the mode a review opens in is `peel.stageMode` for the reviewer who is
// always on one of them. Nothing else peel binds is a setting — the rest of the
// keymap is not a preference, it is the same review everywhere.
type StageMode string

const (
	// StageModeFile is `s` on the whole file the cursor is in: `git add`, one path
	// at a time. It is the size the diff is dealt with in more often than not, so
	// it is what a review opens on.
	StageModeFile StageMode = "file"
	// StageModeHunk is `s` on the one hunk the cursor is in, leaving the rest of
	// the file out of the index — the file that holds a change you are finished
	// with and one you are not. Pressing it twice over takes the whole file.
	StageModeHunk StageMode = "hunk"
)

// StageModeKey is the git config setting behind it. Git stores the last
// component of a key lower-cased, so `peel.stageMode` is what is written and
// this is what comes back.
const StageModeKey = ConfigSection + ".stagemode"

// DefaultStageMode is what `s` takes with nothing written.
func DefaultStageMode() StageMode { return StageModeFile }

// Other is the mode `S` switches to. There are two of them, so a switch has only
// one place to go.
func (m StageMode) Other() StageMode {
	if m == StageModeHunk {
		return StageModeFile
	}
	return StageModeHunk
}

// OrDefault fills in a mode left unset, so a caller holding nothing does not get
// a key that stages nothing.
func (m StageMode) OrDefault() StageMode {
	if m == "" {
		return DefaultStageMode()
	}
	return m
}

// stageModeValues names what the setting takes, for the error a typo gets.
const stageModeValues = `"file" or "hunk"`

// ParseStageMode reads the setting's value.
func ParseStageMode(s string) (StageMode, error) {
	switch mode := StageMode(strings.ToLower(strings.TrimSpace(s))); mode {
	case StageModeFile, StageModeHunk:
		return mode, nil
	}
	return "", fmt.Errorf("%q is not %s", s, stageModeValues)
}

// StageMode reads the setting from git config, most specific file last, the way
// every other peel setting is read.
//
// A value peel does not understand is worth saying and not worth refusing a
// review over: the mode stays the default, the reviewer is told which setting was
// not read, and the pass goes on. It is read once at startup with the moves,
// since staging draws before it asks git anything and a config read on the
// keypress would be the one thing it waited for.
func (a *App) StageMode(ctx context.Context) (StageMode, error) {
	cfg, err := a.Repo.ConfigSection(ctx, ConfigSection)
	if err != nil {
		return DefaultStageMode(), err
	}
	raw := strings.TrimSpace(cfg[StageModeKey])
	if raw == "" {
		return DefaultStageMode(), nil
	}
	mode, err := ParseStageMode(raw)
	if err != nil {
		return DefaultStageMode(), fmt.Errorf("%s: %v", StageModeKey, err)
	}
	return mode, nil
}
