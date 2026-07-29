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
thing again* in `git add -p`, re-deciding hunk by hunk what you already decided
five minutes ago.

`peel` is one pass. You read a hunk, and if it's good you press `s` and it's
staged. Same screen, same keystroke flow.

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
reason this exists — if a tool ships hunk-staging inside a review UI before this
is built, abandon the build and use that instead.

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
| **Staging mechanism** | git plumbing (`git apply --cached`) | Same mechanism `git add -p` uses. No libgit2 dependency |
| **Agent → index** | **Read-only. No flag, no escape hatch** | Claude Code can already run `git add` directly, so `peel hunks add` adds no capability — only a way for things to enter the index unreviewed |
| **Comment store** | JSON at `.git/peel/comments.json` | Per-repo, survives restarts, invisible to `git status`, readable with no daemon running |
| **Agent hookup** | CLI + `SKILL.md` | Agent pulls on demand. Deliberately *not* hunk's live-daemon model — see §6 |
| **Walkthrough** | shell out to `claude -p` | Uses existing Claude Code auth. No API key, no model config, no separate billing |
| **PR mode** | shell out to `gh` | No auth code to own |
| **PR comments** | Local store + explicit `peel pr submit` | Reviewing a PR never writes to GitHub. Posting is a separate, confirmed command |
| **Scope** | All 6 phases (§7) | Including line-level staging |

Deferred, not rejected: jj/Sapling support; web frontend (keep `internal/git`
UI-agnostic so it stays possible).

---

## 4. Architecture

```
peel/
  main.go              CLI entry — TUI by default, subcommands for the agent
  internal/git/        diff parse, hunk model, staging engine   ← UI-agnostic
  internal/tui/        bubbletea models and views
  internal/store/      comments.json + walkthrough cache
  internal/gh/         PR fetch and review submit, via `gh`
  internal/ai/         walkthrough via `claude -p`
  skills/peel-review/SKILL.md
```

`internal/git` must not import anything from `internal/tui`. It's the part
worth getting right, and the part a future web frontend would reuse.

---

## 5. The staging engine — the actual hard part

Everything else is UI. This is where the bugs live.

### The two diffs

Git has three states, and two diffs between them:

```
   HEAD  ──────────────►  index  ──────────────►  worktree
         git diff --cached        git diff
         "staged changes"         "unstaged changes"
```

- **Stage** a hunk = take it from the `git diff` side, `git apply --cached`
- **Unstage** a hunk = take it from `git diff --cached`, `git apply --cached --reverse`

### Core operation

```
1. build a minimal patch: file header (---/+++) + the selected hunk(s) only
2. pipe to:  git apply --cached --unidiff-zero --whitespace=nowarn -
3. re-run git diff / git diff --cached, rebuild state from scratch
```

Step 3 is not optional. Never mutate the in-memory model to reflect what you
think the apply did — re-read from git.

### Gotchas

Each one is a real bug if missed. Phase 2 is not done until all of these are
handled.

| Gotcha | Handling |
|---|---|
| **Stale offsets after apply** | Applying hunk 1 invalidates offsets for hunks 2..n computed from the original diff. Batch all selected hunks into **one** patch and apply once. Safer and faster than re-diffing between each |
| **Renames** | Rename-detected diffs emit `similarity index` / `rename from` headers that `git apply` handles inconsistently. Generate staging patches with `--no-renames` |
| **Untracked files** | Have no diff to apply. `git add -N` (intent-to-add) first so they show up as an all-`+` diff, or special-case to a plain `git add` |
| **Binary files** | Can't hunk-stage. File-level only; grey out hunk actions in the UI |
| **`\ No newline at end of file`** | Must be preserved verbatim in the generated patch or the apply corrupts the file's final byte |
| **Line-count recomputation** | For *line-level* staging you rewrite the hunk: unselected `-` lines become context ` `, unselected `+` lines are dropped — then `@@ -a,b +c,d @@` counts must be recomputed. Getting this wrong silently corrupts the index |
| **CRLF / `core.autocrlf`** | Generate patches from `git diff` output as-is. Never re-encode line endings |
| **Partial-stage same file** | One file can be simultaneously staged *and* unstaged. The file list needs a tri-state indicator (`●` partial), not a checkbox |

### Hunk IDs

Format borrowed from `critique` — makes the CLI scriptable:

```
src/main.go:@-10,6+10,7
```

IDs shift whenever the tree changes. Recompute after every apply; never cache
across operations.

### Test corpus

A fixture repo containing:

1. a rename
2. a binary file
3. an untracked file
4. a file with no trailing newline
5. a file that is both partially staged *and* partially modified

Phase 2 ships when all five behave correctly.

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
```

**No `peel hunks add`.** Read-only by design — see §3.

The flow this is built for:

```
you (TUI):  c → "this leaks the tx"      s → stage the good hunks
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
| **2** | **Stage/unstage hunk and file** | The differentiator. Not done until the §5 test corpus passes |
| **3** | Comment store, `comment list --json`, `SKILL.md` | Now it beats `hunk` for the daily loop |
| **4** | `walkthrough` via `claude -p`, cached to `.git/peel/` | Rendered in a TUI pane |
| **5** | PR mode: `gh pr diff` to read, `peel pr submit` to post | Staging is meaningless for a PR — read-only there |
| **6** | Line-level staging, side-by-side toggle, watch mode, syntax highlighting (chroma) | Line-level staging is the risky one; `lazygit` is the reference implementation |

---

## 8. Keybindings (draft)

Vim-ish, close enough to `hunk` and `lazygit` to not need learning.

| Key | Action |
|---|---|
| `j` / `k` | next / previous hunk |
| `J` / `K` | next / previous file |
| `s` | stage hunk (or file, in the file pane) |
| `u` | unstage hunk / file |
| `a` | stage all |
| `c` | comment on the hunk under the cursor |
| `\` | toggle unified ↔ side-by-side |
| `w` | open the walkthrough pane |
| `?` | help |
| `q` | quit |

Not final — settle after phase 1 is usable.

---

## 9. Prior art

| Repo | What to take |
|---|---|
| `remorses/critique` | Hunk-ID scheme and the `git apply --cached` engine — the only one that solves the gap |
| `umputun/revdiff` | Go TUI structure, annotation→stdout contract, Homebrew/deb/rpm packaging |
| `modem-dev/hunk` | Agent CLI surface; skill file is on disk locally |
| `nilbuild/diffity` | Comment-resolution UX and tour format |
| `jesseduffield/lazygit` | Battle-tested line-level staging in Go — reference for phase 6 |

---

## 10. Still open

- Distribution — Homebrew tap, or `go install` only?
- Does `peel pr submit` post as a single GitHub review, or as individual comments?
- Watch mode (phase 6): poll, or fsnotify?
