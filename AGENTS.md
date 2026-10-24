# o-inventory: agent guide

The family's derived inventory and changelog (`o-inventory`, so `o inv` and `o inventory`). An oden app with an ochazuke runtime behind `ochazuke/cli`. Design: `~/proj/matomeru/docs/plans/inventory.md`. Every fact is read from a source that must already be right (a tool's `catalog`, nabe's registry, `o-proj list`, the hub's `o/` links, `git log`); nothing is hand-written. A wrong fact is fixed in its tool.

## Where does X live

| Concern | File |
|---|---|
| `Tool`, `main`, the oden app, `mount`, paths (hub, registry, config) | `main.go` |
| Operations (snapshot, diff, changelog, check, hook, daily, repos) and their flag parsing | `commands.go` |
| Collection: PATH tools, `BIN catalog`, framework and mounted nouns, nabe hosts, projects, o-story state | `collect.go` |
| Row type, JSON Lines read/write | `rows.go` |
| Snapshot files, lock, hub commits | `snapshot.go` |
| Detection rules (`compared`), `Diff`, change rows | `diff.go` |
| Changelog: concepts, line grammar, folds, commits, merge into files | `changelog.go` |
| Repos config (`repos.json`, seeded) | `repos.go` |
| Drift check | `check.go` |
| Post-commit hook and the launchd agent | `hook.go`, `daily.go` |
| The runtime: row nouns, list/show, `inventory state` | `shell.go` |

## Invariants

- Read-only toward every tool, registry and store; writes only `~/hub/inventory/`, `~/hub/CHANGELOG.md`, each repo's `CHANGELOG.md` (`--write-repos`), its hook block and `repos.json`.
- A snapshot is written only when a compared field differs from `latest.jsonl`; ignored fields (registeredAt, binMtime, descriptions) never make one.
- Changelog lines start with `added:`/`changed:`/`removed:`/`fixed:`; a `changed:` line has `; was`; `fixed:` only from `fix:` commit subjects. `TestChangelogEntryGrammarAndRepoFiles` pins it.
- Framework nouns (no application verb, cli.Mount's rule) are recorded once under `ochazuke`.
- Every list verb declares `.Rows(noun)`; no key bindings here; never bind function keys.
- Every commit: `gofmt -l . && go vet ./... && go test -race ./...` green, `go run . catalog` answers, `o proj check`.
