package app

import (
	"context"
	"fmt"
	"slices"
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

// Keys is which key stages the file the cursor is in and which stages the one
// hunk it is in.
//
// They are one setting rather than two independent ones because they are the
// same decision at two sizes, and which of them deserves the unshifted key
// depends on the pass: a review that goes hunk by hunk wants `s` on the hunk, and
// one that reads whole files and stages them wants it on the file. Nothing else
// peel binds is a setting — the rest of the keymap is not a preference, it is the
// same review everywhere.
type Keys struct {
	StageFile string
	StageHunk string
}

// StageFileKey and StageHunkKey are the git config settings behind them, each
// naming a key rather than a behaviour. Git stores the last component of a key
// lower-cased, so `peel.stageFile` is what is written and this is what comes
// back.
const (
	StageFileKey = ConfigSection + ".stagefile"
	StageHunkKey = ConfigSection + ".stagehunk"
)

// DefaultKeys is what peel does with neither setting written: `s` on the hunk,
// which is the size a diff is read in and the smaller of the two decisions, and
// the same key shifted on the file around it. Pressing the unshifted key twice
// reaches the file as well, so the shift is a convenience rather than the only
// way there.
func DefaultKeys() Keys { return Keys{StageFile: "S", StageHunk: "s"} }

// OrDefault fills in whichever of the two was left unset, so a caller holding
// half a setting does not get a key that stages nothing.
func (k Keys) OrDefault() Keys {
	d := DefaultKeys()
	if k.StageFile == "" {
		k.StageFile = d.StageFile
	}
	if k.StageHunk == "" {
		k.StageHunk = d.StageHunk
	}
	return k
}

// ParseKey reads one key setting's value.
//
// A key is written the way peel writes its own: the character it is, or a name
// with the modifier in front of it. taken is the keys the review already binds,
// which a setting cannot have — a stage key that quietly took `c` would leave a
// reviewer pressing it for a note and staging instead.
func ParseKey(s string, taken []string) (string, error) {
	key := strings.TrimSpace(s)
	if key == "" {
		return "", fmt.Errorf("no key")
	}
	if strings.ContainsAny(key, " \t") {
		return "", fmt.Errorf("%q is more than one key", s)
	}
	// A terminal has no way to say shift and a letter: it sends the capital. A
	// setting written the other way would never fire and never say why.
	if mod, rest, ok := strings.Cut(key, "+"); ok &&
		strings.EqualFold(mod, "shift") && len([]rune(rest)) == 1 && strings.ToLower(rest) != strings.ToUpper(rest) {
		return "", fmt.Errorf("%q arrives as the capital — write %q", s, strings.ToUpper(rest))
	}
	if slices.Contains(taken, key) {
		return "", fmt.Errorf("%q is already bound to something else", key)
	}
	return key, nil
}

// Keys reads both settings from git config, most specific file last, the way
// every other peel setting is read. taken is the keys the review binds itself,
// which neither setting may take over.
//
// Setting one of them to the other's default swaps the pair: there are two keys
// and two things to stage, so `peel.stageFile s` has only one reading, and
// making the reviewer write both halves of a swap to get it would be asking them
// to say the same thing twice. Writing both to the same key is a different
// thing — it says what the other key does nowhere at all — so it is refused and
// both defaults stand.
func (a *App) Keys(ctx context.Context, taken []string) (Keys, error) {
	keys := DefaultKeys()
	cfg, err := a.Repo.ConfigSection(ctx, ConfigSection)
	if err != nil {
		return keys, err
	}

	var bad []string
	set := map[string]string{}
	for _, key := range []string{StageFileKey, StageHunkKey} {
		raw := strings.TrimSpace(cfg[key])
		if raw == "" {
			continue
		}
		parsed, err := ParseKey(raw, taken)
		if err != nil {
			bad = append(bad, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		set[key] = parsed
	}

	if file, hunk := set[StageFileKey], set[StageHunkKey]; file != "" && file == hunk {
		bad = append(bad, fmt.Sprintf("%s and %s are both %q", StageFileKey, StageHunkKey, file))
		set = map[string]string{}
	}

	if file, ok := set[StageFileKey]; ok {
		keys.StageFile = file
	}
	if hunk, ok := set[StageHunkKey]; ok {
		keys.StageHunk = hunk
	}
	if keys.StageFile == keys.StageHunk {
		if _, ok := set[StageFileKey]; ok {
			keys.StageHunk = DefaultKeys().StageFile
		} else {
			keys.StageFile = DefaultKeys().StageHunk
		}
	}

	if len(bad) > 0 {
		return keys, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return keys, nil
}
