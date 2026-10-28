<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img alt="o-inventory" src="assets/logo-light.svg" width="414">
  </picture>
  <br>
  <a href="go.mod"><img alt="Go version" src="https://img.shields.io/github/go-mod/go-version/unixsuperhero/o-inventory"></a>
  <a href="LICENSE"><img alt="License" src="https://img.shields.io/github/license/unixsuperhero/o-inventory"></a>
</p>

<p align="center">The family's derived inventory and changelog: every tool's nouns, verbs and keys, snapshotted when they change, and CHANGELOGs rendered from the diffs, never written by hand.</p>

<p align="center"><img alt="o-inventory over a throwaway hub: a snapshot, a new build of an invented tool, the diff, the changelog entry it renders, the hub's commits and the verb table" src="assets/demo.gif" width="720"></p>

o-inventory (`o inv` and `o inventory` under [oden](https://github.com/unixsuperhero/oden)'s `o`) reads every `o-*` tool on PATH (and `mtm`) through its `catalog`, nabe's registry, `o-proj list` and the hub links, and writes one JSON Lines snapshot per change to `~/hub/inventory/<UTC stamp>.jsonl` (plus `latest.jsonl`). The changelog is rendered from those snapshots, never written by hand. It is an oden app with an [ochazuke](https://github.com/unixsuperhero/ochazuke) runtime behind `ochazuke/cli`, so every row kind is also a noun you can list and show.

## Install

Module `github.com/unixsuperhero/o-inventory`; requires Go 1.26 or newer.

```sh
go install github.com/unixsuperhero/o-inventory@latest
```

or, from a checkout:

```sh
go build -o ~/bin/o-inventory .
```

`go.mod` requires ochazuke (`v0.16.0`) and oden (`v0.1.1`) at their tags. An untracked `go.work` (`use . ../ochazuke ../oden`) builds against the sibling checkouts in `~/proj`; `GOWORK=off` builds from the tags as a fresh clone does.

## Usage

```sh
o inv snapshot [--commit] [--quiet]    # write a snapshot when anything changed
o inv diff [A] [B] [--jsonl] [--all]    # previous vs latest by default
o inv changelog [--repo X] [--write-repos] [--commit]   # --write-repos never commits outside the hub
o inv check                             # drift (stale-build, unreachable, ...); exit 1 when anything is wrong
o inv overview [--json]                 # the latest snapshot as one tree: libs, root, tools, hosts, projects
o inv hook install [REPO|--all]         # post-commit hook in family repos
o inv daily plist|install|uninstall|status|run   # the launchd job (below)
o inv errors [--md P] [--jsonl P] ROOT [PATTERN...]  # a Go module's error sites (Markdown on stdout unless --md)
o inv repos                             # o proj's manifest (~/hub/repos.json) merged with ~/.config/o-inventory/repos.json
o inv tool list | o inv verb show --target verb:o-ai/skill.list | o inv state
o inv story list | o inv edge list | o inv table list   # also lib, dep, noun, binding, page, host, project, change
```

The demo above, as text. Two invented tools, `o-tea` and `o-miso`, are on PATH; a new build of `o-tea` drops `leaf.show` and adds `leaf.steep` with a key:

```console
$ o-inventory snapshot --commit
wrote ~/hub/inventory/20261028T021408Z.jsonl: 15 rows, 15 changes
committed in ~/hub: inventory: 20261028T021408Z (15 changes)
$ o-inventory overview
libs (0)
root (0)
tools (2)
  o-miso    1 noun 2 verbs
  o-tea     1 noun 3 verbs
hosts (0)
projects (0)
$ cp ~/o-tea-0.2.0 ~/bin/o-tea
$ o-inventory snapshot --commit
wrote ~/hub/inventory/20261028T021409Z.jsonl: 17 rows, 5 changes
committed in ~/hub: inventory: 20261028T021409Z (5 changes)
$ o-inventory diff
+ binding:o-tea/normal/leaf/t
+ param:o-tea/leaf.steep/minutes
~ tool:o-tea  version: "0.1.0" -> "0.2.0"
- verb:o-tea/leaf.show
+ verb:o-tea/leaf.steep
$ o-inventory changelog --commit
~/hub/CHANGELOG.md: 2 entries, 2 new
committed in ~/hub: changelog: 20261028T021409Z
$ sed -n 5,15p ~/hub/CHANGELOG.md
## 2026-10-28 02:14:09

### Tools
- changed: `o-tea` version `0.2.0`; was `0.1.0`

### Nouns and verbs
- removed: `o-tea leaf show`
- added: `o-tea leaf steep`, params `--minutes`

### Keybindings
- added: `t` in o-tea normal, `leaf.steep`
```

Every list noun prints a table from its columns:

```console
$ o-inventory verb list --format table
tool      noun  verb   kind         rows  description
o-miso    bowl  list   application  bowl
o-miso    bowl  stir   application
o-tea     leaf  brew   application
o-tea     leaf  list   application  leaf
o-tea     leaf  steep  application
ochazuke  app   quit   interaction
```

Runtime nouns use shared ochazuke metadata and oden contextual help: `o inv tool`, `o inv tool --help`, and `o inv help tool` list the noun's verbs without running them; `o inv tool show --help` describes its arguments. Unknown verbs list the available children. `o inv state` remains the shortcut for `o inv inventory state`.

## Concepts

**Snapshots.** A snapshot is every row collected at one moment, one JSON object per line. It is written only when a compared field differs from `latest.jsonl`; ignored fields (`registeredAt`, `binMtime`, descriptions) never make one. `diff` compares two snapshots (`--all` compares every field too) and prints `+`, `-` and `~` lines, or change rows with `--jsonl`.

**Rows** (`{kind, id, ...}`, id `<kind>:<path>`):

- `lib`: oden, ochazuke and every repos.json module no tool is built from: path, commit, dirty, version, goModule.
- `dep`: a tool's or lib's go.mod on a lib: mode `replace`|`require`, version.
- `shadow`: a claim an `o` command shadows.
- `tool`, including `o`: `root: true` for a root of its own (its catalog has oden's `command` noun, or a repos.json entry's `roots` names it, seeded `"roots": ["mtm"]` on matomeru); `reachable: false` when `o`'s `command state` does not run it and it is no root.
- `noun`, `verb`, `param`, `binding`, `page`, `host`, `project`.
- `story`: o-story's `story state`: `area`, `name`, `file` as `stories/AREA/NAME.json`, `skip` the `gap: ...` reason of a skipped story, read with `story show`.
- `edge`: a mode edge of the core stories: `from`, `to`, `via`.
- `table`: a table declared in mtm's store, from the read-only `mtm store tables`: `tool`, `name`, `owner`, `key`, `version`, `appendOnly`. Row counts are not recorded; an mtm whose catalog lacks the verb is not asked and the snapshot warns.

Framework nouns are recorded once under `ochazuke`.

**The changelog.** One entry per snapshot, newest first, with only the concepts that changed: Root and libraries, Tools, Nouns and verbs, Keybindings, Pages, Hosts, Projects, Stories and mode edges, Store, and last Commits, which comes from git rather than the diff. Every line starts with `added:`, `changed:` (with `; was`), `removed:`, `fixed:` (only from `fix:` commit subjects) or `shadow:` (a claim an `o` command newly shadows).

**Read-only.** o-inventory never writes to a tool, a registry or a store. It writes only `~/hub/inventory/`, `~/hub/CHANGELOG.md`, each repo's `CHANGELOG.md` (with `--write-repos`, never committed), its hook block, `repos.json`, and the daily job's plist, wrapper and logs, and it commits only in the hub. `$O_INV_HUB` moves the hub from `~/hub`.

## In the family

- **The o-\* tools and mtm** are what it inventories: each answers `catalog`, and a wrong fact is fixed in the tool that reports it, never here. **ochazuke**'s framework nouns, shared by every tool built on it, are recorded once under `ochazuke`.
- **The CHANGELOGs.** `changelog` merges every entry into `~/hub/CHANGELOG.md`. `changelog --write-repos` also writes each family repo's `CHANGELOG.md` with the lines about that repo (its tool's nouns, verbs, keys and pages; ochazuke for the framework's; o-story's for stories and edges) and that repo's own commits, and leaves the file uncommitted for review; `--repo X` prints one repo's. Those files are generated: fix a wrong line in its source, not in the file.
- **o-proj.** `o proj new` installs this tool's post-commit hook in a new repository (when o-inventory is on PATH); the hook snapshots after a commit touching `*.go`, `go.mod`, `stories/` or a catalog golden. `o inv hook install REPO|--all` adds it by hand. Projects are `o-proj list`'s rows (manifest entries; `dir` is the path).
- **nabe** hosts are read from nabe's registry (`$XDG_DATA_HOME/nabe/catalogs`), and `check` reports stale or missing ones.

Repositories: o proj's manifest (`~/hub/repos.json`, `$O_PROJ_MANIFEST`) is read first and merged by name with `~/.config/o-inventory/repos.json`, which keeps `roots` and the directories `hook install` adds and is the only repos file written here; without a manifest the repos.json alone is used.

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

## Logo and demo

The logo is the diff glyph, a plus over a minus in seal vermilion: every changelog line is something added or something gone. `python3 docs/logo/build.py` writes `assets/icon.svg`, `assets/logo-light.svg` and `assets/logo-dark.svg`, with the wordmark drawn in the family's lettering (ochazuke's and matomeru's; `--check` confirms the glyphs still redraw theirs). `vhs docs/vhs/demo.tape` records `assets/demo.gif` in a throwaway home; see [docs/vhs/README.md](docs/vhs/README.md).

MIT license.
