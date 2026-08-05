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
| **Staged files fold away** | `s` collapses the file, moves to the next one still open; `space` reopens it | The list left open is the list still to review, so the diff shrinks as the pass goes on, and the one key that ends a file also starts the next. Collapsing is display only — `space` reads a staged file back without touching the index. Revised 2026-08-04: the move looks at the fold and not the index, so a file staged outside peel is still somewhere the pass stops — its diff has not been read here, and skipping it hid changes an agent had staged |
| **Folding is the same decision without the index** | `space` folds a file away and moves on exactly as `s` does | Not every file read is a file to stage — a read-only session has none, and a working tree has files you have looked at and left alone. Folding is how a pass records what has been read, so it moves on the way staging does |
| **A part-staged file reads as two halves** | a heading rules across the pane above each one — `staged · already in the index` and `unstaged · not in the index yet` — and the index's half opens folded, `space` on the heading showing it | Added 2026-08-05. git can put one file in both places at once, and peel drew the two changes as one run of hunks with the word `index` or `worktree` at the end of each hunk header. Fifty green `+` lines that are already staged, then four that are not, read as one change of fifty-four: the new work is at the bottom of something that looks reviewed, and nothing on screen says where the reviewed part stopped. The heading is a break rather than a label because that is the actual question — where does one change end — and the fold answers it by removing the reviewed half from the pass, which is the same rule `s` follows on a whole file. A fully staged file keeps its diff on screen: folding the only thing in it would leave a header with nothing under it. The file header carries the split too (`index +47 -0  worktree +4 -0`), since a folded file shows nothing else |
| **A note records which diff it is numbered against** | `origin`: `index` or `worktree`, on the comment | Added 2026-08-05. The two halves of a part-staged file are measured against different files, so both have a line 12 and they are not the same line. Anchoring on `file:line:side` alone put every note written on the working tree onto whatever the index happened to hold at that number — several lines away, in code the reviewer had not been reading. The old anchor is not narrowed, it is completed: a note with no origin is one written before the distinction existed, and still lands where it always did |
| **A file changed after it was staged opens again** | a fold made by `s` reopens on the reload that finds working-tree changes in it; a fold made by `space` does not | Added 2026-08-05. Staging says "done with this file" about the changes that went into the index and nothing about the ones that arrive afterwards — and a `✓` in the tree is the one place a change can hide from a pass, since the pass skips what is folded. What opens is the new work alone: the index's half stays folded, so the file shows exactly what arrived. A file put away with `space` was put away deliberately and stays that way, which is the difference between the two keys that otherwise do the same thing |
| **Folds persist** | JSON at `.git/peel/folds.json`, per target | A pass through a large diff rarely finishes in one sitting, and reopening to a diff that has forgotten every file already read starts the pass again. Folds of files no longer in the diff are dropped, so the next change to a file is not hidden by a fold left from the last one |
| **Changes are drawn before they are written** | optimistic, with rollback: the screen moves on the keypress, git is asked behind it | Added 2026-07-30. A stage is `git add` plus a full re-read — 50ms in a small repository, several hundred in a large one — and the answer is never in doubt, only slow. Waiting for it before redrawing makes a decision that has already been made look like peel thinking about it, and staging is a key pressed file after file. The guess is a prediction of a whole-file stage and nothing cleverer; the re-read behind it stays authoritative, and a write that fails restores the screen it was drawn over. Two rules keep the guess from being seen: writes are queued, so peel's own git calls cannot race for the index lock, and a read-back landing while another write is out is dropped rather than undrawing it. `q` waits for the writes it has already reported |
| **What `o` opens a file with** | git config: `peel.open` for everything, `peel.open.<extension>` to override it for one kind of file, the desktop opener when neither is set | Added 2026-07-31. The desktop opener is the right default and the wrong answer for source: a repository is mostly code, which belongs in the editor, and mostly-not-code — a screenshot, a PDF, a `.md` worth reading rendered — which belongs to whatever the desktop already sends it to. One command for the whole tree cannot be both, so the setting is per extension with a fallback under it. It lives in git config rather than a config file of peel's own because peel has no config file and does not want one: git config is already per-user with a per-repository override, already in the reviewer's dotfiles, and already something peel shells out to. Read on each `o` rather than at startup, so changing it lands in the session already open. Split on spaces, not run through a shell — `open -a Marked` works, `cmd && other` does not, and nothing in a config value reaches a shell |
| **Agent → index** | **Read-only. No flag, no escape hatch** | Claude Code can already run `git add` directly, so `peel hunks add` adds no capability — only a way for things to enter the index unreviewed |
| **Comment store** | JSON at `.git/peel/comments.json` | Per-repo, survives restarts, invisible to `git status`, readable with no daemon running |
| **Two reviews, one store** | every comment carries an author; `A` hides the agent's, `X` clears them, `peel comment clear --author agent` does the same from the CLI | Added 2026-07-30. An agent's pass and the reviewer's own notes land in the same file, and only the reviewer's are irreplaceable. Nothing an agent writes can overwrite one — `add` only ever appends — so deleting is the only way to lose one, and the agent's review is removable as a group without a key that can reach a note the reviewer wrote |
| **Hiding the agent's review persists** | JSON at `.git/peel/view.json`, per target | Added 2026-08-05. `A` is a decision about what is worth looking at, and folds already establish that such a decision outlives the process: a diff read without the agent's notes had them back on the next run, which is the same startle as a pass that has forgotten every file already read. It is kept out of `folds.json` because it is not the same kind of record — a fold says what has been read and is dropped when the file leaves the diff, a view says what the diff is filtered down to and has nothing to go stale against. A review with no agent notes in it opens plain whatever is written down: there is nothing to take out, and `A` says as much rather than lifting a filter, so honouring one would leave a header claiming notes are hidden and no key to disagree with it |
| **Agent hookup** | CLI + `SKILL.md` | Agent pulls on demand. Deliberately *not* hunk's live-daemon model — see §6 |
| **Walkthrough** | pluggable CLI providers: `claude -p` or `codex exec` | Reuses existing Claude Code or Codex auth. Claude stays the default; `--provider` selects explicitly |
| **Walkthrough shape** | grouped, numbered steps in reading order | A reviewer wants to be walked through the changed code, not handed a summary of the changeset they can already see |
| **Walkthrough is parsed, not printed** | steps → files → explanation, parsed out of the markdown | A wall of markdown is something you read *about* the change. Parsing markdown rather than demanding JSON keeps `peel walkthrough` useful as prose |
| **Walkthrough is the diff, not a pane** | the steps reorder the files and each explanation sits above the files it covers | A separate pane is a map you read and then leave; the same notes inside the diff are read *with* the code they describe, and there is one thing to navigate instead of two |
| **Every changed file lands in a group** | leftovers collected under "Not grouped" | The order is the reviewer's map of the change. A file the model forgot to mention would otherwise be a file the reviewer never learns to read |
| **The file pane is a tree** | directories with their files under them, drawn only — no key enters it, folds it, or moves in it | Added 2026-08-03. A flat list of paths answers "what changed" and not "where", and it answers it badly: at 14 to 30 columns every path is shortened from the left, so a screen of `…tui/model.go` spends its width on the directories and truncates the one segment that identifies the file. The tree spends the width once per directory instead of once per file, which is what leaves room for whole file names. Rows stay in the document's order, so the pane still reads top to bottom with the diff and a walkthrough's ordering survives; a directory takes the position of the first file inside it. A directory with one way down is joined onto what is below it — `internal/tui`, not two rows and an indent — since a level that only ever leads to one place costs a row and says nothing. It stays display-only for the same reason the pane has never been enterable: the cursor lives in the diff, and a second place to be would double every navigation key. A directory carries the state of the files under it, `✓` once every one of them is staged, which is the one thing a tree can say that a list cannot |
| **Long lines** | **scroll sideways, never wrap** | Added 2026-07-30. Every row renders to exactly one terminal line, and the cursor, the window and the file pane beside it all count in rows — wrapping would make one document row several screen rows and put that arithmetic wrong everywhere. A horizontal offset leaves it untouched: the same rows are on screen, further along. `h`/`l` slide the code, `0`/`$` reach the ends |
| **The gutter does not scroll** | line numbers and the `+`/`-` origin stay pinned; only the code slides | What a row scrolled out to column 90 still has to say is which line it is and whether it was added. A row that has lost both is unreadable long before its tail is worth reaching |
| **Follow mode** | on by default; `f` toggles, `--no-watch` opts out | The common case is reviewing while an agent or editor is still changing the working tree |
| **Review base** | `--rev <ref>` moves the base, never the far side | Added 2026-07-30. "Everything since HEAD~2" is one diff of the last two commits plus the uncommitted work on top, not a commit range: the side being reviewed is the working tree in every mode peel has. The base is resolved to a hash once, so a commit landing mid-session cannot move it |
| **`--rev` is read-only** | `Stageable: false` for any base behind HEAD | HEAD is the only base staging means anything against. A file whose change is part committed cannot be `git add`ed into the shape on screen, so `s` would either lie or do nothing. `--rev HEAD` resolves to the working-tree session rather than a read-only copy of it |
| **`--rev` comments target the working tree** | same scope as a plain `peel` | Moving the base changes how far back the diff reaches, not which code the notes are about. Scoping them to the base would hide them from `peel comment list` and from the agent that has to address them |
| **PR mode** | shell out to `gh` | No auth code to own |
| **PR comments** | Local store + explicit `peel pr submit` | Reviewing a PR never writes to GitHub. Posting is a separate, confirmed command |
| **Scope** | All 6 phases (§7) | Line-level staging shipped in phase 6 and was then removed — see the granularity decision above |
| **Distribution** | Homebrew tap, prebuilt binaries, skill in the keg | Added 2026-07-30. A tag builds four archives — darwin and linux, both architectures — and rewrites a formula in `ziadalzarka/homebrew-tap`, so installing is one command and needs no Go toolchain. A formula rather than a cask: a cask is macOS-only, and an unsigned one has to strip `com.apple.quarantine` in a post-install hook to run at all, which is a Gatekeeper bypass Apple can close whenever it likes. GoReleaser dropped formula support in v2.16, so the formula is generated by `.github/scripts/formula.sh` and the whole release is `make dist` — the same command locally as in CI, which is what makes it checkable before it is tagged. A formula cannot write into `~/.claude/skills`, so the skill ships at `libexec/skills/peel-review` and the caveats give the one `ln -s` that hooks it up |

| **peel says when it is out of date** | one line on the way out — the release and `brew upgrade ziadalzarka/tap/peel` — checked at most once a day, off with `PEEL_NO_UPDATE_CHECK` | Added 2026-08-05. Brew only upgrades what someone asks it to, so a reviewer who installed peel once stays on that version until they think to check, and nothing in a local tool ever prompts the thought. The cost has to be nothing: the request goes out while the review is open and is spoken about only after peel has quit, so it is never between the reviewer and the diff, and quitting waits a fraction of a second for it at most. It is the only thing peel sends anywhere without being asked, which is why it is one unauthenticated GET to the releases API, cached in the user's cache directory for a day — failures cached too, so a machine with no network asks once a day rather than every run — and silent about everything except a version newer than the running one. A build from a checkout is never checked: its version does not parse as a release, and whoever built it does not need telling about brew |

Deferred, not rejected: jj/Sapling support; web frontend (keep `internal/git`
UI-agnostic so it stays possible).

---

## 4. Architecture

```
peel/
  main.go              CLI entry — TUI by default, subcommands for the agent
  internal/git/        diff parse, hunk model, status, staging   ← UI-agnostic
  internal/tui/        bubbletea models and views
  internal/store/      comments.json, folds.json, view.json + walkthrough cache
  internal/gh/         PR fetch and review submit, via `gh`
  internal/ai/         walkthrough via `claude -p`
  internal/update/     is a newer release out — the one thing that leaves the machine unasked
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
1. move the file's side in the in-memory model, draw it   (the guess)
2. git add -- <path>              (stage)
   git restore --staged -- <path> (unstage)
3. re-run git diff / git diff --cached, rebuild state from scratch   (the truth)
```

Step 3 is not optional, and step 1 never survives it. The model is moved ahead of
the write because a whole-file stage is predictable — that is the only reason it
can be drawn before git has been asked — but the guess is never allowed to stand:
what the reviewer ends up looking at came from git, and a write that fails puts
back the screen it was drawn over. See the optimistic-updates decision in §3.

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
| **Partial-stage same file** | peel can no longer create it, but git can. One file can be simultaneously staged *and* unstaged, so the file list needs a tri-state indicator (`●` partial), not a checkbox — and `s` on it stages the working-tree half without disturbing the index half. In the diff the two halves are drawn under their own headings, the index's folded away, and a note left on either records which one it was numbered against — see §3 |
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
peel comment clear [--author agent]                 # its own review, not the user's
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

### The paste path

An agent in the repository reads the store. An agent in a browser tab, or on
another machine, has no store to read — so `C` renders the same notes as text and
puts them on the system clipboard:

```
Review comments copied from peel. Review them one by one.

internal/tui/model.go:412
  this leaks the tx
```

Written to be pasted into a conversation, not parsed: one block per note, the
anchor in the `file:line` form every tool prints, the side named only when it is
the old one, and no IDs or timestamps — they mean nothing to a reader that cannot
look them up. It copies the reviewer's own notes, never the agent's, on screen or
not: an agent's notes are what a review was already given for, not a review to
hand back out. Resolved notes are left out too, with a count in the footer:
sending an agent after work already done is worse than sending it after less.

Reaching the clipboard means shelling out, the way `o` does — `pbcopy`, `wl-copy`,
`xclip`, `xsel` or `clip.exe`, whichever is on `PATH` first. Not OSC 52: the
terminal is bubbletea's while peel is running, and writing an escape sequence
around its renderer to save a subprocess is not a trade worth making.

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

The file tree on the left is a map, not a pane you move into — but it is on
screen by default and scrolls on its own window, so a long tree can be read past
without moving the diff. It lays the changed files out under the directories
they live in, so a name in it comes with where that name is. Its mark follows
the diff window rather than the cursor: it names the file the window opens on,
which is the file being read, and the directories above that file are undimmed
so the mark reads as a place in the tree. `b` hands its width to the diff and
takes it back, for a change whose lines are longer than the pane leaves room
for.

`↓`/`↑` step the cursor a line at a time and the window follows; `j`/`k` jump it
between whole things — the next hunk, file or comment. The wheel scrolls the
window and drags the cursor with it, so the cursor never addresses a row that has
left the screen. Mouse reporting is on, so the wheel arrives as a wheel event
rather than as whatever arrow keys the terminal would emulate.

A line wider than the pane is scrolled to rather than wrapped. `h`/`l` slide the
code sideways by one indent a press, `0` and `$` reach the first column and the
end of the longest line in the diff, and the header names the column while the
window is away from the first one — a screen of short lines scrolled past their
ends otherwise reads as a diff that has lost its code. The line numbers and the
`+`/`-` do not move: they are what a row still has to say when its text has gone.
The offset belongs to the window rather than the cursor, so it survives moving
between files and, unlike the wheel, sliding it drags nothing — the same rows are
on screen either way. A horizontal wheel does the same thing in the terminals
that report one; `h`/`l` are the path that always works.

| Key | Action |
|---|---|
| `↓` / `↑` | move the cursor one line, diff body included |
| `j` / `k` | next / previous hunk, file or comment |
| wheel | scroll the diff, dragging the cursor along |
| `]` / `[` | next / previous file — from inside a file, to its header first; the window opens on the file |
| `}` / `{`, wheel over the pane | scroll the file tree |
| `h` / `l`, horizontal wheel | scroll the code sideways, one indent per press |
| `0` / `$` | back to the first column / out to the longest line's end |
| `b` | hide or show the file tree, giving the diff the whole width |
| `g` / `G` | first / last row |
| `ctrl+d` / `ctrl+u` | half a page down / up |
| `space` | fold the file away and move on to the next, or expand it again |
| `s` | stage the file the cursor is in, folding it away and moving to the next |
| `u` | unstage that file, opening it again |
| `a` / `U` | stage everything, folding it all away / unstage everything, opening it all |
| `o` | open the file the cursor is in, outside peel — with `peel.open.<extension>`, `peel.open`, or the desktop opener |
| `c` | comment at the cursor |
| `enter` / `alt+enter` | in the editor: save the comment / write another line |
| `x` | resolve or reopen the comment at the cursor |
| `D` | delete the comment at the cursor |
| `C` | copy your own comments as text, to paste into an agent |
| `A` | hide or show the comments an agent left, leaving the reviewer's own |
| `X` | delete every agent comment, after asking |
| `\` | toggle unified ↔ side-by-side |
| `w` | walkthrough on / off |
| `W` | regenerate the walkthrough |
| `r` | reload from git |
| `?` | help |
| `q` | quit |

The walkthrough is not a screen of its own. It is the same diff, with the files
in the order the narrative reads them and each step's explanation sitting above
the files it covers — so the thing the reviewer scrolls through is the code, with
the notes in place. `j`/`k` stop on a note the way they stop on a hunk, `]`/`[`
land on the note that introduces the next file rather than skipping past it,
`space` folds a note away once it has been read, and `w` again puts the diff back
in git's order. Staging keeps the notes; when the code itself moves on under them
the header says `stale` and `W` writes new ones.

Staging folds and moves on. `s` stages the file the cursor is in, collapses it and
opens the next file still open at the top of the window — so what is left open is
what is left to review, the diff shrinks as the pass goes on, and one key both
ends a file and starts the next. Folded files are skipped on the way: each has
been read, and a header with nothing under it is nothing to carry the pass on
with. Being staged is not what makes a file done — the fold is, so a file staged
outside peel and never folded here is a stop like any other, since its diff has
still to be read. Only when nothing below
is still open does the cursor stay where it is, on the file just dealt with
rather than on an arbitrary one — a file left open above does not pull it back,
since the pass runs down the diff and what is behind the cursor was left open on
purpose. The fold is display only:
`space` reads a staged file back, and `u` opens it again on its way out of the
index. A stage that fails leaves the file open and the cursor on it, because it
still has to be dealt with.

All of that happens on the keypress, before git has been asked anything. Staging
a file, unstaging one, writing a note or resolving one is never in doubt — only
slow — so the screen is moved to what the change is about to make true, the write
goes on behind it, and the re-read that follows only confirms what is already
there. Nothing shows a banner for it, because there is nothing to wait for. What
fails comes back: the file opens again, the note leaves the diff, and the footer
says why. `q` pressed straight after a change waits for that change to be
written rather than taking the process down with it in flight.

A hunk header and a diff line are still cursor stops and still addressable — they
are what `j`/`k` step between and what a comment anchors to. They are just not
staging units: `s` on either one stages the file around it. A side-by-side row
can hold a removal beside the addition that replaced it; a comment on it lands on
the new side.

The heading over each half of a part-staged file is a stop and a mark too, and
`space` on the index's one shows what is already staged or puts it away again.
`space` on the working tree's half is refused and says why: that half is what is
left to review, and folding it would leave a file that says it changed and shows
nothing.

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

- Does `peel pr submit` post as a single GitHub review, or as individual comments?
- Watch mode (phase 6): poll, or fsnotify?
