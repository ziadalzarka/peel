package ai

import (
	"fmt"
	"strings"
)

// defaultInstruction is the task given to a provider when the caller does not
// supply one. It asks for orientation rather than a restatement of the diff:
// the diff is already on screen, so a list of what changed adds nothing.
const defaultInstruction = `Write a short walkthrough of this changeset for the engineer who is about to review it.

Structure it as markdown:

## What changed
Two or three sentences on the intent of the change as a whole — what it is
trying to accomplish, not a file-by-file list.

## Worth a close look
The two or three specific places a reviewer should slow down, each with the
file path and why it matters: risky logic, a changed invariant, error handling,
anything load-bearing.

## Questions
Anything genuinely ambiguous from the diff alone. Omit this section if there is
nothing real to ask.

Be concrete and reference actual identifiers and paths. Do not restate the diff
line by line — the reviewer can already see it. Keep it under 400 words.`

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
