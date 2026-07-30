---
name: peel-review
description: Reads and annotates a peel diff review — lists hunks and their staged state, leaves inline review comments the user sees in the TUI, and generates a walkthrough of a changeset. Use when the user is reviewing a diff with peel, asks for a code review of their working tree, or wants comments left on specific lines.
---

# Peel Review

peel is an interactive terminal diff reviewer. The TUI is for the user — never
run bare `peel`, `peel --rev` or `peel --pr` with no subcommand, which would take
over their terminal. Use the subcommands below; they read and write the same
store the TUI does, and work whether or not the TUI is open.

There is no daemon and no session to attach to. State lives in `.git/peel/`, so
every command just needs to run inside the repository.

## Workflow

```text
1. peel hunks list --json                # what changed, and what is already staged
2. read the actual code with your normal tools
3. peel comment add ...                  # one note per thing worth saying
4. peel comment list --json              # confirm what the user will see
```

## peel never stages

peel does not stage from the command line, on purpose. `peel hunks add`,
`stage`, `unstage` and `rm` all refuse and point here.

Staging is the user's decision, made in the TUI against a file they just read,
and it is whole-file: peel stages files, never hunks or lines. If you need to
stage something, use `git add` — say so first, and only when the user asked for
it.

## Inspect

```bash
peel hunks list [--json] [--file <path>] [--staged | --unstaged]
peel comment list [--json] [--file <path>] [--unresolved] [--author user|agent] [--all]
peel providers
```

`hunks list --json` returns one object per hunk:

```json
{
  "id": "internal/git/status.go:@-10,6+10,7",
  "file": "internal/git/status.go",
  "staged": false,
  "header": "@@ -10,6 +10,7 @@ func LoadStatus",
  "added": 3,
  "removed": 1,
  "section": "func LoadStatus"
}
```

- A partially staged file appears twice — once with `"staged": true` for what is
  in the index, once with `false` for what is not. Both are real; the split comes
  from git, since peel itself only stages whole files.
- `id` is derived from line offsets, so **it changes whenever the tree changes**.
  Re-run `hunks list` after anything touches the index; never reuse an ID from an
  earlier call.
- Binary files get one entry with `"binary": true` and no line counts.

`comment list --json` returns `id`, `file`, `line`, `side`, `body`, `hunk`,
`author`, `resolved`, `createdAt`, `target`.

By default both commands are scoped to what is being reviewed: the working tree,
or the pull request named by `--pr`. `comment list --all` ignores that scope.

## Reviewing further back than HEAD

`--rev <ref>` moves the base of the diff back. It is a global flag, so it goes
before the subcommand and works with all of them:

```bash
peel --rev HEAD~2 hunks list --json          # the last two commits *and* the uncommitted work
peel --rev origin/main hunks list --json     # everything on this branch
peel --rev HEAD~2 walkthrough
```

Use it when the user asks about work that is already committed — `peel hunks
list` alone only sees what is uncommitted, so a review of "the changes I made
today" will silently miss most of them.

`<ref>` is anything git resolves to a commit: `HEAD~2`, a hash, a branch. Two
things behave differently in these sessions:

- **`"staged"` is meaningless.** Everything reports `false`, because a change
  committed since the base is neither staged nor unstaged. Don't report staged
  state from a `--rev` run, and don't pass `--staged`/`--unstaged`.
- **Comments are shared with the working tree**, not scoped to the base — the
  side being reviewed is the working tree either way. A note left under `--rev`
  is the same note `peel comment list` shows without it.

## Comment

```bash
peel comment add --file <path> --line <n> --body "..." [--side new|old] [--hunk <id>] [--json]
peel comment add --file <path> --body "..."                       # file-level note
peel comment resolve <id>... [--undo]
peel comment rm <id>...
peel comment clear [--file <path>] [--resolved] [--author user|agent] [--all]
```

- `--file` is required. `--line` is 1-based on `--side`; omit it for a note about
  the file as a whole.
- `--side new` (the default) anchors to the changed file. Use `--side old` to
  comment on a line being deleted — the new file has no line number for it.
- `--author` defaults to `agent`, which is what marks the note as yours in the
  TUI. Don't pass `--author user`.
- Pass `--hunk <id>` when you have it: the TUI can still show the comment in
  context after line numbers move.
- The body can come from stdin instead of `--body`, which is easier for anything
  multi-line or containing quotes:

```bash
printf 'This drops the error.\n\nWorth returning it instead.\n' \
  | peel comment add --file internal/git/status.go --line 42
```

## The user's comments are not yours to delete

One store holds both reviews. Yours are `"author": "agent"` and the user's are
`"author": "user"`, and `rm` and `clear` do not know the difference unless you
tell them — `peel comment clear` with no flags wipes the user's notes along with
your own, which is a review they cannot get back.

So: **only ever remove your own.**

```bash
peel comment clear --author agent          # your review, and nothing else
peel comment list --json --author agent    # check before rm-ing an id
```

Nothing you write can overwrite a user comment — `add` always appends a new one
— so deleting is the only way to lose one. When the user asks you to clear
comments and does not say whose, clear yours and say so; if they meant all of
them, that is `--author user` as a second, deliberate command.

The user has the same distinction in the TUI: `A` hides your comments and shows
theirs, and `X` deletes every one of yours after asking. A review of yours that
is in their way is one keypress from being gone, so leaving fewer, better notes
is worth more than covering everything.

## Walkthrough

```bash
peel walkthrough [--regen] [--provider <name>] [--prompt "..."]
```

Generates a step-by-step walkthrough of the changeset and caches it. It shells
out to an AI provider, so it is not free — read the cached one unless the diff has
changed, in which case peel regenerates it for you. `--regen` forces a new one.

The output groups the changed files into numbered steps ordered so each sets up
the next, starting at whatever the change rests on and following it out to the
surface, then closes with `## Worth a close look` and `## Questions`. It walks
the changed code rather than summarising the changeset, so it is a reasonable way
to orient yourself in an unfamiliar diff before reading hunks.

Each step is a `## N. Title` heading followed by a line of nothing but its
backticked file paths, then the explanation:

```markdown
## 1. The type everything else reads
`internal/git/hunk.go` · `internal/git/parse.go`

`Hunk` gains a `NoNewline` line kind…
```

peel parses that line to group the files in its TUI, so a `--prompt` replacing
the instruction should keep the shape if the result is meant to stay navigable.
`--prompt` replaces the default entirely rather than appending to it.

## Pull requests

```bash
peel --pr <ref> hunks list --json
peel --pr <ref> comment add --file <path> --line <n> --body "..."
peel pr view <ref> [--json]
```

`<ref>` is a number, `owner/repo#number`, or a URL. Comments on a PR are stored
locally and scoped to that PR — nothing is sent anywhere.

**Do not run `peel pr submit`.** It posts the review to the forge, where other
people see it and it cannot be undone from here. It is the user's call, it
prompts them for an explicit yes, and `--yes` exists for them, not for you. If a
review is ready to post, say so and let them run it.

`peel pr submit --dry-run` is safe — it prints the exact payload and exits — but
prefer `comment list --json`, which tells you the same thing without the risk of
a mistyped flag.

## Reviewing well

Your job is to say the things the user would not spot themselves.

- Read `hunks list --json` for structure first, then open the files. A hunk
  header is not enough context to review from.
- Comment on intent, risk, and consequence — not on what the diff already shows.
  "This drops the error" is useful; "renamed x to y" is not.
- Anchor to the most specific line you can defend. A file-level note is right for
  a file-level concern and a cop-out for a line-level one.
- Don't comment on every hunk. A review with four real findings beats one with
  twenty observations.
- One concern per comment. The user resolves them one at a time.

## Common errors

- **"peel does not stage from the command line"** — expected. See above.
- **"peel must run inside a git repository"** — `cd` into the repo first.
- **"no provider available"** — `peel providers` shows what is missing:
  `claude` or `codex` for walkthroughs, `gh` for pull requests.
- **"comment add needs --file"** / **"needs --body, or text on stdin"** — the
  anchor and the body are both required.
