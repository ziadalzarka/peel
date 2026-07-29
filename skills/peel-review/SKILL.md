---
name: peel-review
description: Reads and annotates a peel diff review — lists hunks and their staged state, leaves inline review comments the user sees in the TUI, and generates a walkthrough of a changeset. Use when the user is reviewing a diff with peel, asks for a code review of their working tree, or wants comments left on specific lines.
---

# Peel Review

peel is an interactive terminal diff reviewer. The TUI is for the user — never
run bare `peel` or `peel --pr`, which would take over their terminal. Use the
subcommands below; they read and write the same store the TUI does, and work
whether or not the TUI is open.

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

Staging is the user's decision, made in the TUI against a hunk they just read.
If you need to stage something, use `git add` — say so first, and only when the
user asked for it.

## Inspect

```bash
peel hunks list [--json] [--file <path>] [--staged | --unstaged]
peel comment list [--json] [--file <path>] [--unresolved] [--author user|agent] [--all]
peel providers
```

`hunks list --json` returns one object per hunk:

```json
{
  "id": "internal/git/patch.go:@-10,6+10,7",
  "file": "internal/git/patch.go",
  "staged": false,
  "header": "@@ -10,6 +10,7 @@ func BuildPatch",
  "added": 3,
  "removed": 1,
  "section": "func BuildPatch"
}
```

- A partially staged file appears twice — once with `"staged": true` for what is
  in the index, once with `false` for what is not. Both are real and separately
  addressable.
- `id` is derived from line offsets, so **it changes whenever the tree changes**.
  Re-run `hunks list` after anything touches the index; never reuse an ID from an
  earlier call.
- Binary files get one entry with `"binary": true` and no line counts.

`comment list --json` returns `id`, `file`, `line`, `side`, `body`, `hunk`,
`author`, `resolved`, `createdAt`, `target`.

By default both commands are scoped to what is being reviewed: the working tree,
or the pull request named by `--pr`. `comment list --all` ignores that scope.

## Comment

```bash
peel comment add --file <path> --line <n> --body "..." [--side new|old] [--hunk <id>] [--json]
peel comment add --file <path> --body "..."                       # file-level note
peel comment resolve <id>... [--undo]
peel comment rm <id>...
peel comment clear [--file <path>] [--resolved] [--all]
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
  | peel comment add --file internal/git/patch.go --line 42
```

## Walkthrough

```bash
peel walkthrough [--regen] [--provider <name>] [--prompt "..."]
```

Generates a narrative of the changeset and caches it. It shells out to an AI
provider, so it is not free — read the cached one unless the diff has changed, in
which case peel regenerates it for you. `--regen` forces a new one.

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
- **"hunk ... not found — the tree changed, reload and retry"** — the ID is
  stale. Re-run `peel hunks list`.
- **"peel must run inside a git repository"** — `cd` into the repo first.
- **"no provider available"** — `peel providers` shows what is missing:
  `claude` for walkthroughs, `gh` for pull requests.
- **"comment add needs --file"** / **"needs --body, or text on stdin"** — the
  anchor and the body are both required.
