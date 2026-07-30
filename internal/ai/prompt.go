package ai

import (
	"fmt"
	"strings"
)

// defaultInstruction is the task given to a provider when the caller does not
// supply one. It asks for a guided tour of the changed code in reading order,
// not a summary: the reviewer wants to be walked through the pieces, so an
// overall description of the changeset's intent adds nothing they cannot get
// from the branch name.
const defaultInstruction = `Walk the engineer through this changeset step by step, so they can read the
changed code in an order that makes sense.

Group the changed files into steps. Each step is one coherent piece of the
change together with the files it touches — usually two or three files that
changed for the same reason. Order the steps so each one sets up the next:
start at the foundation the change rests on (a type, a parsed structure, a
schema) and follow it outward to the code that consumes it, ending at the
surface the user or caller sees. Put a file in two steps if it genuinely
changed for two different reasons.

Every file in the changed-files list below must appear in at least one step.
The steps are the reviewer's map of the change, so a file missing from all of
them is a file they will not know to read.

Write one markdown section per step, numbered. The line under the heading is
nothing but the step's file paths, each in backticks, separated by " · " — it
is read by the tool, so keep prose off it:

## 1. A short title for what this step does
` + "`path/to/file.go`" + ` · ` + "`path/to/other.go`" + `

Then walk through the code itself. Name the functions, types, fields and
constants that changed, and say what the new code does that the old code did
not: the field that was added and what reads it, the branch that now handles a
case that used to fall through, the call that moved, the condition that
inverted. When the diff changes behaviour, describe the behaviour rather than
the syntax — say what input now takes a different path, not that an if
statement was added. Where a step only makes sense given something in an
earlier step, say so explicitly. Aim for 60 to 150 words per step.

After the steps, close with these two, unnumbered and with no path line:

## Worth a close look
The two or three places a reviewer should slow down, each with its file path
and the reason: risky logic, a changed invariant, error handling that swallows
something, anything load-bearing.

## Questions
Anything genuinely ambiguous from the diff alone. Omit this section entirely if
there is nothing real to ask.

Do not open with a summary of the changeset as a whole, and do not restate the
diff line by line — the reviewer can already see it and wants to be guided
through it. Reference real identifiers and real paths throughout.

Use as many steps as the change needs, up to about seven. Prefer five clear
steps over one crowded one, but on a large changeset group more coarsely rather
than emitting a section per file: fold the mechanical follow-on edits — call
sites updated for a renamed symbol, generated files, test fixtures moving with
the code they cover — into the step that caused them, and say that is what they
are instead of walking each one. Folding a file in still means listing it on
that step's path line.`

// MaxDiffBytes caps how much diff is sent to a provider. A very large changeset
// would otherwise blow past context limits and fail; truncating produces a
// partial narrative, which is more useful than an error.
const MaxDiffBytes = 200_000

// BuildPrompt renders the full prompt text for a request.
func BuildPrompt(req Request) string {
	instruction := req.Instruction
	if strings.TrimSpace(instruction) == "" {
		instruction = defaultInstruction
	}

	var b strings.Builder
	b.WriteString(instruction)
	b.WriteString("\n\n")

	if req.Target != "" {
		fmt.Fprintf(&b, "Reviewing: %s\n\n", req.Target)
	}
	if len(req.Files) > 0 {
		fmt.Fprintf(&b, "Files changed (%d):\n", len(req.Files))
		for _, f := range req.Files {
			b.WriteString("- ")
			b.WriteString(f)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	diff, truncated := truncateDiff(req.Diff, MaxDiffBytes)
	b.WriteString("Diff:\n\n```diff\n")
	b.WriteString(diff)
	if !strings.HasSuffix(diff, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("```\n")
	if truncated {
		b.WriteString("\nNote: the diff was truncated at a file boundary because of its size. " +
			"Say so in the walkthrough rather than implying you saw all of it.\n")
	}
	return b.String()
}

// truncateDiff shortens a diff to at most limit bytes, cutting at a file
// boundary so the provider never receives a half-parsed hunk.
func truncateDiff(diff string, limit int) (string, bool) {
	if len(diff) <= limit {
		return diff, false
	}

	head := diff[:limit]
	// Prefer the last complete file; fall back to the last complete line.
	if idx := strings.LastIndex(head, "\ndiff --git "); idx > 0 {
		return head[:idx+1], true
	}
	if idx := strings.LastIndexByte(head, '\n'); idx > 0 {
		return head[:idx+1], true
	}
	return head, true
}
