# peel

A terminal diff reviewer that stages what you just reviewed.

Every local diff-review tool is read-only, so reviewing and `git add -p` are two
separate passes over the same diff. `peel` is one pass: read a hunk, press `s`,
it's staged. Comments you leave land in a store Claude Code can read.

```
$ peel                                  # review the working tree
$ peel --pr 412                         # review a GitHub PR
$ peel comment list --json              # what the agent reads
$ peel walkthrough                      # AI narrative of the changeset
```

**Status:** spec only, nothing built. See [SPEC.md](SPEC.md).

Go 1.26+, bubbletea TUI, single static binary. `gh` required for PR mode,
`claude` for walkthroughs.
