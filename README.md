# o-inventory

The family's derived inventory and changelog. It reads every `o-*` tool on PATH (and `mtm`) through its `catalog`, nabe's registry, `o-proj list` and the hub links, and writes one JSON Lines snapshot per change to `~/hub/inventory/<UTC stamp>.jsonl` (plus `latest.jsonl`). The changelog is rendered from those snapshots, never written by hand.

```sh
go build -o ~/bin/o-inventory .
o inv snapshot [--commit] [--quiet]    # write a snapshot when anything changed
o inv diff [A] [B] [--jsonl] [--all]    # previous vs latest by default
o inv changelog [--repo X] [--write-repos] [--commit]
o inv check                             # drift; exit 1 when anything is wrong
o inv hook install [REPO|--all]         # post-commit hook in family repos
o inv daily install                     # launchd agent; prints the launchctl load command
o inv repos                             # ~/.config/o-inventory/repos.json
o inv tool list | o inv verb show --target verb:o-ai/skill.list | o inv state
```

Rows (`{kind, id, ...}`, id `<kind>:<path>`): `tool`, `noun`, `verb`, `param`, `binding`, `page`, `host`, `project`, `story`, `edge`. Framework nouns are recorded once under `ochazuke`.

Until ochazuke and oden are tagged, `go.mod` replaces them with the local checkouts in `~/proj`.
