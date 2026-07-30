# peel

A terminal diff reviewer that stages what you just reviewed.

Every local diff-review tool is read-only, so reviewing and `git add` end up as
two separate passes over the same diff: you read it in a viewer, then walk the
whole thing again in the terminal, re-deciding what you decided five minutes ago.

`peel` is one pass. Read a file, press `s`, and it is staged and folded away —
what is left open is what is left to review. Comments you leave land in a store
Claude Code can read, so "address my review comments" needs no copy-paste.

![peel reviewing its own working tree](docs/screenshots/review.png)

The file list on the left is the review queue and a `✓` marks a staged file. The
footer is the whole keymap.

## Install

```sh
make install          # builds and installs to /opt/homebrew/bin
go build -o peel .    # or just build it here
```

Go 1.26+, one static binary, no runtime. `gh` is required for PR mode; either
`claude` or `codex` can write walkthroughs. `peel providers` shows what is
available.

## Use

```sh
peel                      # review the working tree
peel --pr 412             # review a GitHub PR (read-only)
peel --no-watch           # don't re-read the repository while open
peel --provider codex     # use Codex for the walkthrough instead of Claude
```

### Keys

| Key | Action |
|---|---|
| `↓` / `↑` | move the cursor one line, diff body included |
| `j` / `k` | next / previous hunk, file or comment |
| `J` / `K` | next / previous file |
| `]` / `[` | scroll the file list on its own |
| `tab` | collapse or expand the file, or fold a walkthrough note away |
| `s` | stage the file the cursor is in, folding it away |
| `u` | unstage that file, opening it again |
| `a` / `U` | stage everything / unstage everything |
| `c` | comment at the cursor, changed line or not |
| `enter` / `alt+enter` | in the editor: save the comment / write another line |
| `x` / `D` | resolve / delete the comment at the cursor |
| `\` | toggle unified and side-by-side |
| `w` / `W` | walkthrough on-off / regenerate it |
| `f` | follow: re-read the repository as it changes |
| `r` | reload from git |
| `?` / `q` | help / quit |

There is one cursor and it rests anywhere — file headers, hunk headers, comments,
and every line of a diff body, changed or not. Nothing to enter first: `s`
anywhere inside a file stages that file, `c` anywhere leaves a note there,
including on the untouched code a change breaks.

`c` opens the editor inline, in the diff, exactly where the comment will sit once
it is saved — the code stays on screen while you write about it, and the cursor
is still on it afterwards. `enter` saves it; `alt+enter` writes another line.

The mouse works too. The wheel scrolls whichever pane it is over and drags the
cursor along, so the cursor never addresses a row that has left the screen.

### Staging is whole-file

A file is the unit a review decision is actually made in, so that is the unit
peel stages: `git add` and `git restore --staged`, one path at a time. No patch
generation, so none of `git add -p`'s failure modes — nothing here can write the
wrong lines into your index. If you want to split a file, `git add -p` already
does that well.

Staging collapses the file, and the fold is display only: `tab` reads a staged
file back without touching the index, and a stage that fails leaves the file open,
because it still has to be dealt with.

### Walkthrough

`w` asks Claude Code (or Codex) to walk you through the change, and the result is
not a separate pane — the diff itself is reordered into the steps the narrative
reads it in, with each step's explanation above the files it covers. So you read
the notes and the code together. `j`/`k` stop on a note like they stop on a hunk,
`tab` folds one away once you have read it, `w` again puts the diff back in git's
order, and `W` writes a new one. Staging keeps the notes; when the code moves on
underneath them the header says `stale`.

Walkthroughs are cached in `.git/peel/`, so reopening does not pay for one twice.

## For agents

`peel hunks list` and `peel comment` are the agent surface, documented for Claude
Code in [`skills/peel-review`](skills/peel-review/SKILL.md).

```sh
peel hunks list --json                  # what changed, and what is staged
peel comment list --json                # what the user wrote while reading
peel comment add --file F --line N --body "..."
peel walkthrough                        # the cached narrative, as markdown
```

Two things it deliberately will not do:

- **Stage.** `peel hunks add`/`stage`/`unstage` refuse. Staging is a decision made
  against a file someone just read, in the UI. Anything scripting git can call
  `git add` directly, so exposing it here would only add a way for changes to
  reach the index unreviewed.
- **Post anything.** `peel pr submit` is the only command that leaves the machine.
  It prints the exact payload, then waits for an explicit `y`.

Comments are written straight through to `.git/peel/comments.json`, so there is no
daemon and no session to attach to: review, quit, *then* ask Claude.

## Layout

```
internal/git       diff parsing, status, and whole-file staging
internal/store     comments and the walkthrough cache, under .git/peel/
internal/ai        walkthrough providers (claude-code, codex)
internal/forge     pull request providers (github, via gh)
internal/registry  provider lookup, shared by both
internal/app       wiring; the only package that names concrete providers
internal/cli       the agent-facing command surface
internal/tui       the review UI
```

`internal/git` never imports `internal/tui` — navigation and rendering are
testable without a terminal, and the git layer stays reusable.

See [SPEC.md](SPEC.md) for the design and the reasoning behind each decision,
including the ones that were reversed.
