# peel

A terminal diff reviewer that stages what you just reviewed.

Every local diff-review tool is read-only, so reviewing and `git add -p` are two
separate passes over the same diff. `peel` is one pass: read a hunk, press `s`,
it's staged. Comments you leave land in a store Claude Code can read.

```
$ peel                                  # review the working tree
$ peel --pr 412                         # review a GitHub PR
$ peel hunks list --json                # what changed, and what is staged
$ peel comment list --json              # what the agent reads
$ peel walkthrough                      # AI narrative of the changeset
```

## Install

```
go build -o peel .
```

Go 1.26+, single static binary. `gh` is required for PR mode, `claude` for
walkthroughs; `peel providers` shows what's available.

## Keys

`j`/`k` move between hunks, `J`/`K` between files. `s` stages the hunk under the
cursor — or the file, on a file header — and `u` unstages it. `v` selects
individual lines. `c` comments, `\` toggles side-by-side, `w` opens the
walkthrough, `?` lists everything.

## For agents

`peel hunks list` and `peel comment` are the agent surface;
[`skills/peel-review`](skills/peel-review/SKILL.md) documents them. Two things
they deliberately don't do:

- **Stage.** `peel hunks add`/`stage`/`unstage` refuse. Staging is a decision
  made against a hunk someone just read, in the UI. Anything scripting git can
  call `git add` directly, so exposing it here would only add a way for changes
  to reach the index unreviewed.
- **Post anything.** `peel pr submit` is the only command that leaves the
  machine. It prints the exact payload, then waits for an explicit `y`.

## Layout

```
internal/git       diff parsing and the staging engine — the risk centre
internal/store     comments and the walkthrough cache, under .git/peel/
internal/ai        walkthrough providers (claude-code)
internal/forge     pull request providers (github, via gh)
internal/registry  provider lookup, shared by both
internal/app       wiring; the only package that names concrete providers
internal/cli       the agent-facing command surface
internal/tui       the review UI
```

See [SPEC.md](SPEC.md) for the design and the reasoning behind each decision.
