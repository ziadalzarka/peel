# peel — build spec

A terminal diff reviewer that can also stage what you just reviewed.

**Status:** spec agreed, nothing built yet.
**Decided:** 2026-07-29.

---

## 1. What this is, from zero

You change some code. Before committing, you want to do two things:

1. **Read** the diff — check it over, notice the bits that are wrong.
2. **Stage** the parts that are good — `git add` the finished work, leave the rest.

Today those are two separate passes over the same diff. You read it in a viewer
(`hunk`, `difit`, GitHub), then you go back to the terminal and walk the *whole
thing again* in `git add -p`, re-deciding file by file what you already decided
five minutes ago.

`peel` is one pass. You read a file, and if it's good you press `s`: it is staged
and folded away, leaving what you have not dealt with yet. Same screen, same
keystroke flow.

```
   today                              with peel
   ─────                              ─────────
   hunk      → read the diff          peel  → read it, stage it, comment on it
   git add -p → decide again
   claude     → paste your notes      claude → peel comment list --json
```

The second thing it does: comments you leave while reading are stored where
Claude Code can read them. You review, you type notes into the diff, you quit,
you tell Claude "address my review comments" — and it pulls them itself. No
copy-paste.

---

## 2. Why build instead of install

Six existing tools were checked on 2026-07-29. Every one of them is read-only,
except the one that can't review properly.

| Tool | view diff | PRs | comments→agent | AI walkthrough | **stage hunks** | **stage files** | runtime |
|---|---|---|---|---|---|---|---|
| **hunk** 0.17 | ✅ | ❌ | ✅ session CLI | notes only | ❌ | ❌ | binary |
| **difit** | ✅ | ✅ `--pr` | ✅ copy-prompt + skill | via skill | ❌ | ❌ | Node ≥21 |
| **diffity** | ✅ | ✅ PR URL | ✅ `/diffity-resolve` | ✅ `/diffity-tour` | ❌ | ❌ | Node |
| **revdiff** | ✅ | ~ via stdin | ✅ annotations→stdout | ✅ CC plugin | ❌ | ❌ | Go binary |
| **codiff** | ✅ | ✅ `codiff pr 75` | ✅ | ✅ `-w` | ❌ | ✅ file only | Electron, macOS |
| **critique** | ✅ | ❌ | ❌ | ✅ | ✅ **by ID** | ✅ | Bun |

```
        review + agent hookup                staging
   ┌───────────────────────────────┐   ┌──────────────────┐
   │  hunk   difit   diffity       │   │                  │
   │  revdiff   codiff             │   │    critique      │
   └───────────────────────────────┘   └──────────────────┘
                        ↑                    ↑
                        └────── peel ────────┘
```

The one tool that writes to the git index (`critique`) is the weakest reviewer
and needs Bun. The good reviewers never touch the index. That gap is the entire
reason this exists — if a tool ships staging inside a review UI before this is
built, abandon the build and use that instead.

The **stage hunks** column is what the survey asked about, and peel deliberately
answers it with "no" — see the staging-granularity decision in §3.

---

## 3. Decisions

Settled 2026-07-29. Each of these was a real fork; recording the reasoning so
it doesn't get relitigated.

| Decision | Choice | Why |
|---|---|---|
| **Name** | `peel` | Binary `peel`, module `github.com/ziadalzarka/peel`, store `.git/peel/` |
| **Form factor** | Terminal TUI | Stays in the terminal, works over ssh, ~30ms start. Browser UI was the alternative — rejected as a context switch |
| **Language** | Go + bubbletea/lipgloss | Go 1.26.4 already installed. One static ~10MB binary, no runtime. `revdiff` proves the shape works |
| **Diff layout** | Unified default, `\` toggles side-by-side | Unified matches `git diff` and fits a narrow terminal; side-by-side for spotting edits inside a long line |
| **Staging granularity** | **whole files only** | Revised 2026-07-30, after building hunk and line staging. A file is the unit a review decision is actually made in, and reviewing one then pressing `s` reads the same either way. Hunk and line staging bought a patch engine whose failure mode is writing the wrong lines into the index, to split a file the way `git add -p` already splits it |
| **Staging mechanism** | `git add` / `git restore --staged` per path | The whole-file decision removes the need to generate patches at all: no `git apply --cached`, no `@@` arithmetic, no intent-to-add dance for untracked files |
| **Staged files fold away** | `s` collapses the file, moves to the next one to review; `tab` reopens it | The list left open is the list still to review, so the diff shrinks as the pass goes on, and the one key that ends a file also starts the next. Collapsing is display only — `tab` reads a staged file back without touching the index |
| **Agent → index** | **Read-only. No flag, no escape hatch** | Claude Code can already run `git add` directly, so `peel hunks add` adds no capability — only a way for things to enter the index unreviewed |
| **Comment store** | JSON at `.git/peel/comments.json` | Per-repo, survives restarts, invisible to `git status`, readable with no daemon running |
| **Agent hookup** | CLI + `SKILL.md` | Agent pulls on demand. Deliberately *not* hunk's live-daemon model — see §6 |
| **Walkthrough** | pluggable CLI providers: `claude -p` or `codex exec` | Reuses existing Claude Code or Codex auth. Claude stays the default; `--provider` selects explicitly |
| **Walkthrough shape** | grouped, numbered steps in reading order | A reviewer wants to be walked through the changed code, not handed a summary of the changeset they can already see |
| **Walkthrough is parsed, not printed** | steps → files → explanation, parsed out of the markdown | A wall of markdown is something you read *about* the change. Parsing markdown rather than demanding JSON keeps `peel walkthrough` useful as prose |
| **Walkthrough is the diff, not a pane** | the steps reorder the files and each explanation sits above the files it covers | A separate pane is a map you read and then leave; the same notes inside the diff are read *with* the code they describe, and there is one thing to navigate instead of two |
| **Every changed file lands in a group** | leftovers collected under "Not grouped" | The order is the reviewer's map of the change. A file the model forgot to mention would otherwise be a file the reviewer never learns to read |
| **Follow mode** | on by default; `f` toggles, `--no-watch` opts out | The common case is reviewing while an agent or editor is still changing the working tree |
| **Review base** | `--rev <ref>` moves the base, never the far side | Added 2026-07-30. "Everything since HEAD~2" is one diff of the last two commits plus the uncommitted work on top, not a commit range: the side being reviewed is the working tree in every mode peel has. The base is resolved to a hash once, so a commit landing mid-session cannot move it |
| **`--rev` is read-only** | `Stageable: false` for any base behind HEAD | HEAD is the only base staging means anything against. A file whose change is part committed cannot be `git add`ed into the shape on screen, so `s` would either lie or do nothing. `--rev HEAD` resolves to the working-tree session rather than a read-only copy of it |
| **`--rev` comments target the working tree** | same scope as a plain `peel` | Moving the base changes how far back the diff reaches, not which code the notes are about. Scoping them to the base would hide them from `peel comment list` and from the agent that has to address them |
| **PR mode** | shell out to `gh` | No auth code to own |
| **PR comments** | Local store + explicit `peel pr submit` | Reviewing a PR never writes to GitHub. Posting is a separate, confirmed command |
| **Scope** | All 6 phases (§7) | Line-level staging shipped in phase 6 and was then removed — see the granularity decision above |

Deferred, not rejected: jj/Sapling support; web frontend (keep `internal/git`
UI-agnostic so it stays possible).

---

## 4. Architecture

```
peel/
  main.go              CLI entry — TUI by default, subcommands for the agent
  internal/git/        diff parse, hunk model, status, staging   ← UI-agnostic
  internal/tui/        bubbletea models and views
  internal/store/      comments.json + walkthrough cache
  internal/gh/         PR fetch and review submit, via `gh`
  internal/ai/         walkthrough via `claude -p`
  skills/peel-review/SKILL.md
```

`internal/git` must not import anything from `internal/tui`. It's the part
worth getting right, and the part a future web frontend would reuse.

---

## 5. Staging

Staging is whole-file. It was not always: a patch engine that staged selected
hunks and selected lines was built, passed the corpus below, and was then removed
on 2026-07-30 — see the granularity decision in §3. What is left is the part that
was never the risk.

### The two diffs

Git has three states, and two diffs between them:

```
   HEAD  ──────────────►  index  ──────────────►  worktree
         git diff --cached        git diff
         "staged changes"         "unstaged changes"
```

peel reads both, per file, and merges them into one entry — which is what lets a
file report `●` partial when git has it in both places at once.

`--rev` reads one diff rather than two: `git diff <base>` spans the index in a
single step, and a change committed since the base is neither staged nor
unstaged. Every file there carries one side, so the tri-state indicator has
nothing to report — which is consistent, since that session cannot stage anyway.

### Core operation

```
1. git add -- <path>              (stage)
   git restore --staged -- <path> (unstage)
2. re-run git diff / git diff --cached, rebuild state from scratch
```

Step 2 is not optional. Never mutate the in-memory model to reflect what you
think the write did — re-read from git.

No patch is generated, so none of the `git apply` hazards apply: no `@@`
recomputation, no `--unidiff-zero` question, no intent-to-add escalation to make
an untracked file addressable, no `\ No newline at end of file` marker to carry
through by hand. `git add` handles a rename half, a binary file, a deletion and
CRLF because that is its job.

### What still has to behave

| Case | Handling |
|---|---|
| **Renames** | Read with `--no-renames`, so a rename is a delete plus an add: two files, staged separately, which reproduces the rename in the index |
| **Untracked files** | Have no diff. Synthesize one with `git diff --no-index /dev/null <path>` for display only, so the contents are reviewable before the index is touched |
| **Binary files** | No diff to show. The row says so, and staging works like any other file |
| **Partial-stage same file** | peel can no longer create it, but git can. One file can be simultaneously staged *and* unstaged, so the file list needs a tri-state indicator (`●` partial), not a checkbox — and `s` on it stages the working-tree half without disturbing the index half |
| **Re-read after every write** | Hunk IDs, line offsets and stage state are all invalidated by any change to the tree, including peel's own writes |

### Hunk IDs

Hunks are read and addressed, never staged: the IDs exist so `peel hunks list`
is scriptable and so the UI can put the cursor back after a reload.

Format borrowed from `critique` — makes the CLI scriptable:

```
src/main.go:@-10,6+10,7
```

IDs shift whenever the tree changes. Recompute after every write; never cache
across operations.

### Test corpus

A fixture repo containing:

1. a rename
2. a binary file
3. an untracked file
4. a file with no trailing newline
5. a file that is both partially staged *and* partially modified

All five still have to behave, and `internal/git/stage_test.go` still covers
them — they are the shapes a working tree actually contains, whatever the staging
granularity is.

---

## 6. Agent contract

The CLI is the source of truth; `SKILL.md` just teaches Claude Code to use it.

```bash
peel comment list [--json] [--file F]        # read your review notes
peel comment add --file F --line N --summary "..."   # agent leaves its own
peel comment rm <id>
peel comment clear
peel hunks list [--json]                     # read-only: what's staged / not
peel walkthrough [--regen]                   # cached markdown narrative
peel --rev <ref> <any of the above>          # measured from an older base
```

**No `peel hunks add`.** Read-only by design — see §3.

The flow this is built for:

```
you (TUI):  c → "this leaks the tx"      s → stage the good files
you (CC):   "address my review comments"
agent:      peel comment list --json  →  fixes  →  peel comment rm <id>
```

### Why not hunk's daemon model

`hunk` exposes a live session over a local daemon (`hunk session comment add
--repo .`), so the agent talks to a *running TUI*. `peel` writes through to
`.git/peel/comments.json` on every mutation instead, so:

- comments survive quitting the TUI — review, quit, *then* ask Claude
- no daemon, no session IDs, no "ask the user to launch it first"
- the agent reads a file, which it is already good at

Cost: the TUI and the agent can both write the store concurrently. The TUI
re-reads before every write and reloads on external change.

Read `/opt/homebrew/opt/hunk/libexec/skills/hunk-review/SKILL.md` before writing
`skills/peel-review/SKILL.md` — the command surface is a good model even though
the session/daemon split doesn't apply.

---

## 7. Build order

Each phase is independently useful — the tool is worth running after phase 2.

| Phase | Deliverable | Notes |
|---|---|---|
| **1** | `git diff` parse → file/hunk/line model; file-list + diff-pane TUI | Read-only. Validates the UI before touching the index |
| **2** | **Stage/unstage file** | The differentiator. Not done until the §5 test corpus passes. Shipped as hunk, line *and* file staging; narrowed to files on 2026-07-30 |
| **3** | Comment store, `comment list --json`, `SKILL.md` | Now it beats `hunk` for the daily loop |
| **4** | `walkthrough` via `claude -p`, cached to `.git/peel/` | Grouped into the diff itself |
| **5** | PR mode: `gh pr diff` to read, `peel pr submit` to post | Staging is meaningless for a PR — read-only there |
| **6** | Line-level staging, side-by-side toggle, watch mode, syntax highlighting (chroma) | Line-level staging shipped here and was removed again — the risk it carried was not worth the granularity |

---

## 8. Keybindings

Vim-ish, close enough to `hunk` and `lazygit` to not need learning.

There is one cursor and it rests anywhere: file headers, hunk headers, comments
and every line of a diff body, changed or not. There is no mode to enter first —
`s` from anywhere inside a file stages that file, and `c` anywhere means a note
there, including on the untouched code a change breaks. Only the blank between
files and the continuation lines of a multi-line comment are skipped.

The file list on the left is a map, not a pane you move into — but it is always
on screen and scrolls on its own window, so a long file list can be read past
without moving the diff. Its mark follows the diff window rather than the cursor:
it names the file the window opens on, which is the file being read.

`↓`/`↑` step the cursor a line at a time and the window follows; `j`/`k` jump it
between whole things — the next hunk, file or comment. The wheel scrolls the
window and drags the cursor with it, so the cursor never addresses a row that has
left the screen. Mouse reporting is on, so the wheel arrives as a wheel event
rather than as whatever arrow keys the terminal would emulate.

| Key | Action |
|---|---|
| `↓` / `↑` | move the cursor one line, diff body included |
| `j` / `k` | next / previous hunk, file or comment |
| wheel | scroll the diff, dragging the cursor along |
| `J` / `K` | next / previous file — from inside a file, to its header first; the window opens on the file |
| `]` / `[`, wheel over the pane | scroll the file list |
| `g` / `G` | first / last row |
| `ctrl+d` / `ctrl+u` | half a page down / up |
| `tab` | collapse or expand the file |
| `s` | stage the file the cursor is in, folding it away and moving to the next |
| `u` | unstage that file, opening it again |
| `a` / `U` | stage everything, folding it all away / unstage everything, opening it all |
| `c` | comment at the cursor |
| `enter` / `alt+enter` | in the editor: save the comment / write another line |
| `x` | resolve or reopen the comment at the cursor |
| `D` | delete the comment at the cursor |
| `\` | toggle unified ↔ side-by-side |
| `w` | walkthrough on / off |
| `W` | regenerate the walkthrough |
| `r` | reload from git |
| `?` | help |
| `q` | quit |

The walkthrough is not a screen of its own. It is the same diff, with the files
in the order the narrative reads them and each step's explanation sitting above
the files it covers — so the thing the reviewer scrolls through is the code, with
the notes in place. `j`/`k` stop on a note the way they stop on a hunk, `J`/`K`
land on the note that introduces the next file rather than skipping past it,
`tab` folds a note away once it has been read, and `w` again puts the diff back
in git's order. Staging keeps the notes; when the code itself moves on under them
the header says `stale` and `W` writes new ones.

Staging folds and moves on. `s` stages the file the cursor is in, collapses it and
opens the next file still to review at the top of the window — so what is left
open is what is left to review, the diff shrinks as the pass goes on, and one key
both ends a file and starts the next. Already-staged files are skipped on the way,
since they are folded away and there is nothing to decide about them. The last
file has nowhere to advance to and keeps the cursor. The fold is display only:
`tab` reads a staged file back, and `u` opens it again on its way out of the
index. A stage that fails leaves the file open and the cursor on it, because it
still has to be dealt with.

A hunk header and a diff line are still cursor stops and still addressable — they
are what `j`/`k` step between and what a comment anchors to. They are just not
staging units: `s` on either one stages the file around it. A side-by-side row
can hold a removal beside the addition that replaced it; a comment on it lands on
the new side.

Commenting is not a screen of its own either. `c` opens the editor in the diff,
at the anchor the comment will attach to — under the line, the hunk header or the
file header the cursor was on — so the code being commented on stays in front of
the reviewer writing about it. The editor opens three rows tall and grows a row
per line written. `enter` saves, since most notes are one line and reaching for a
chord to finish one is the wrong default; `alt+enter` writes another line and
`esc` cancels. Either way the cursor is left on what it was on: a note written is
not progress through the diff, and neither is resolving one with `x`.

---

## 9. Prior art

| Repo | What to take |
|---|---|
| `remorses/critique` | Hunk-ID scheme; and the `git apply --cached` engine, before staging narrowed to whole files |
| `umputun/revdiff` | Go TUI structure, annotation→stdout contract, Homebrew/deb/rpm packaging |
| `modem-dev/hunk` | Agent CLI surface; skill file is on disk locally |
| `nilbuild/diffity` | Comment-resolution UX and tour format |
| `jesseduffield/lazygit` | Battle-tested line-level staging in Go — the reference if partial staging is ever wanted back |

---

## 10. Still open

- Distribution — Homebrew tap, or `go install` only?
- Does `peel pr submit` post as a single GitHub review, or as individual comments?
- Watch mode (phase 6): poll, or fsnotify?
