# o-inventory

The family's derived inventory and changelog. It reads every `o-*` tool on PATH (and `mtm`) through its `catalog`, nabe's registry, `o-proj list` and the hub links, and writes one JSON Lines snapshot per change to `~/hub/inventory/<UTC stamp>.jsonl` (plus `latest.jsonl`). The changelog is rendered from those snapshots, never written by hand.

```sh
go build -o ~/bin/o-inventory .
o inv snapshot [--commit] [--quiet]    # write a snapshot when anything changed
o inv diff [A] [B] [--jsonl] [--all]    # previous vs latest by default
o inv changelog [--repo X] [--write-repos] [--commit]   # --write-repos never commits outside the hub
o inv check                             # drift; exit 1 when anything is wrong
o inv hook install [REPO|--all]         # post-commit hook in family repos
o inv daily plist|install|uninstall|status|run   # the launchd job (below)
o inv repos                             # ~/.config/o-inventory/repos.json
o inv tool list | o inv verb show --target verb:o-ai/skill.list | o inv state
```

Rows (`{kind, id, ...}`, id `<kind>:<path>`): `tool`, `noun`, `verb`, `param`, `binding`, `page`, `host`, `project`, `story`, `edge`. Framework nouns are recorded once under `ochazuke`.

## Daily job

A launchd agent, `com.unixsuperhero.o-inventory`, in your `gui/$UID` domain, runs at 09:00 local:

```sh
o inv daily plist [--out PATH] [--hour H --minute M]   # print (or write) the plist; nothing else
o inv daily install [--hour H --minute M]              # wrapper, dirs, plist, plutil -lint, launchctl bootstrap, print
o inv daily status                                     # plist present? loaded? last run, next run
o inv daily run                                        # run the job now, in the foreground
o inv daily uninstall                                  # launchctl bootout; remove the plist and the wrapper; keep the logs
```

The plist (`~/Library/LaunchAgents/com.unixsuperhero.o-inventory.plist`) runs `/bin/sh ~/.local/share/o-inventory/daily.sh` with `PATH` (~/bin, the Go bin dir, Homebrew, system) and `MTM_SERVER=default`, `RunAtLoad` false. The wrapper runs `o-inventory snapshot --commit --quiet && o-inventory changelog --write-repos --commit --quiet` and appends one line per run (`TIMESTAMP ok|failed (exit N) in Ns`) to `~/Library/Logs/o-inventory/daily.log`; launchd's own output goes to `stdout.log` and `stderr.log` beside it. Only the hub commits: each repo's `CHANGELOG.md` is written and left uncommitted for review. `install` is idempotent (a loaded job is booted out, then bootstrapped again); it uses `launchctl bootstrap`/`bootout`/`print`, never the deprecated `load`/`unload`.

Until ochazuke and oden are tagged, `go.mod` replaces them with the local checkouts in `~/proj`.
