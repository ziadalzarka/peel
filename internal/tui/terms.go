package tui

type terms struct {
	stage, unstage     string
	staged, unstaged   string
	in, out            string
	half, otherHalf    string
	stagedSide         string
	workSide           string
	stagedAll          string
	unstagedAll        string
	fileMode, hunkMode string
	fileHint, hunkHint string
	unstageHint        string
	fileHelp, hunkHelp string
	switchHelp         string
	unstageHelp        string
	allHelp            string
}

var stagingTerms = terms{
	stage:       "stage",
	unstage:     "unstage",
	staged:      "staged",
	unstaged:    "unstaged",
	in:          "in the index",
	out:         "out of the index",
	half:        "index",
	otherHalf:   "worktree",
	stagedSide:  "staged · already in the index",
	workSide:    "unstaged · not in the index yet",
	stagedAll:   "staged everything",
	unstagedAll: "unstaged everything",
	fileMode:    "s stages the whole file the cursor is in",
	hunkMode:    "s stages the hunk the cursor is in — twice over takes the whole file",
	fileHint:    "s stage file · S stage by hunk",
	hunkHint:    "s stage hunk · S stage by file",
	unstageHint: "u unstage",
	fileHelp:    "stage the file the cursor is in — it folds away and the next one opens",
	hunkHelp:    "stage the hunk the cursor is in — twice over takes the whole file",
	switchHelp:  "switch what s stages: the whole file, or the hunk the cursor is in",
	unstageHelp: "unstage that file, opening it again",
	allHelp:     "stage everything / unstage everything",
}

var viewingTerms = terms{
	stage:       "mark viewed",
	unstage:     "unmark",
	staged:      "viewed",
	unstaged:    "unmarked",
	in:          "viewed",
	out:         "left to view",
	half:        "viewed",
	otherHalf:   "unviewed",
	stagedSide:  "viewed",
	workSide:    "not viewed yet",
	stagedAll:   "marked everything viewed",
	unstagedAll: "unmarked everything",
	fileMode:    "s marks the whole file the cursor is in viewed",
	hunkMode:    "s marks the hunk the cursor is in viewed — twice over takes the whole file",
	fileHint:    "s mark viewed · S by hunk",
	hunkHint:    "s mark hunk viewed · S by file",
	unstageHint: "u unmark",
	fileHelp:    "mark the file the cursor is in viewed — it folds away and the next one opens",
	hunkHelp:    "mark the hunk the cursor is in viewed — twice over takes the whole file",
	switchHelp:  "switch what s marks viewed: the whole file, or the hunk the cursor is in",
	unstageHelp: "unmark that file, opening it again",
	allHelp:     "mark everything viewed / unmark everything",
}

func termsFor(viewing bool) terms {
	if viewing {
		return viewingTerms
	}
	return stagingTerms
}

func (m *Model) terms() terms {
	return termsFor(m.session != nil && m.session.PR != nil)
}

func (d Document) terms() terms { return termsFor(d.viewing) }
