package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/unixsuperhero/oden"
)

// flags are a command's parsed words: booleans and valued options by name
// (--name, --name=value, --name value for valued ones) and the bare words,
// in any order.
type flags struct {
	bools  map[string]bool
	values map[string]string
	words  []string
}

// parseFlags reads args against the command's boolean and valued option
// names; any other --word is an error.
func parseFlags(cmd string, args, bools, valued []string) (flags, error) {
	f := flags{bools: map[string]bool{}, values: map[string]string{}}
	isBool, isValued := set(bools), set(valued)
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			f.words = append(f.words, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			f.words = append(f.words, a)
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(a, "-"), "=")
		switch {
		case isBool[name] && !hasValue:
			f.bools[name] = true
		case isValued[name] && hasValue:
			f.values[name] = value
		case isValued[name] && i+1 < len(args):
			i++
			f.values[name] = args[i]
		default:
			return f, fmt.Errorf("%s: unknown or incomplete option %q (options: %s)", cmd, a, optionList(bools, valued))
		}
	}
	return f, nil
}

func set(names []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func optionList(bools, valued []string) string {
	var out []string
	for _, b := range bools {
		out = append(out, "--"+b)
	}
	for _, v := range valued {
		out = append(out, "--"+v+" VALUE")
	}
	if len(out) == 0 {
		return "none"
	}
	return strings.Join(out, ", ")
}

// commands are the operations: inline oden commands, each parsing its own
// words.
func (t *Tool) commands() []oden.Command {
	return []oden.Command{
		{Name: "snapshot", Summary: "collect every source; write ~/hub/inventory/STAMP.jsonl and latest.jsonl when they differ (--commit commits in the hub, --quiet)", Run: func(ctx context.Context, args []string) error {
			f, err := parseFlags("snapshot", args, []string{"commit", "quiet"}, nil)
			if err != nil {
				return err
			}
			_, err = t.Snapshot(ctx, f.bools["commit"], f.bools["quiet"])
			return err
		}},
		{Name: "diff", Summary: "the changes between snapshots A and B (stamps, latest or paths; default the previous and the latest): readable, or --jsonl change rows; --all compares every field", Run: func(ctx context.Context, args []string) error {
			f, err := parseFlags("diff", args, []string{"jsonl", "all"}, nil)
			if err != nil {
				return err
			}
			return t.DiffCommand(f.words, f.bools["jsonl"], f.bools["all"])
		}},
		{Name: "changelog", Summary: "merge the snapshots' entries into ~/hub/CHANGELOG.md (--repo X prints one repo's; --write-repos also each repo's CHANGELOG.md, never committed (left for review); --commit commits only the hub's; --quiet)", Run: func(ctx context.Context, args []string) error {
			f, err := parseFlags("changelog", args, []string{"write-repos", "commit", "quiet"}, []string{"repo"})
			if err != nil {
				return err
			}
			return t.Changelog(ctx, ChangelogOptions{Repo: f.values["repo"], WriteRepos: f.bools["write-repos"], Commit: f.bools["commit"], Quiet: f.bools["quiet"]})
		}},
		{Name: "repos", Summary: "the family repos (" + "$XDG_CONFIG_HOME/o-inventory/repos.json, seeded from ~/hub/o and ochazuke, oden, matomeru on first use)", Run: func(ctx context.Context, args []string) error {
			if _, err := parseFlags("repos", args, nil, nil); err != nil {
				return err
			}
			return t.ReposCommand()
		}},
		{Name: "check", Summary: "drift: tools without a catalog, stale or missing nabe hosts, missing project trees, repos without the hook (--jsonl); exit 1 when anything is wrong", Run: func(ctx context.Context, args []string) error {
			f, err := parseFlags("check", args, []string{"jsonl"}, nil)
			if err != nil {
				return err
			}
			return t.Check(ctx, f.bools["jsonl"])
		}},
		{Name: "overview", Summary: "the latest snapshot as one tree: libs, root, tools, hosts, projects (--json: the tree as data; inventory overview)", Run: func(ctx context.Context, args []string) error {
			f, err := parseFlags("overview", args, []string{"json"}, nil)
			if err != nil {
				return err
			}
			return t.OverviewCommand(f.bools["json"])
		}},
		{Name: "hook", Summary: "hook install [REPO|--all]: the post-commit hook that snapshots after a commit touching *.go, go.mod, stories/ or a catalog golden (idempotent; appends to an existing hook)", Run: func(ctx context.Context, args []string) error {
			return t.HookCommand(args)
		}},
		{Name: "daily", Summary: "daily plist|install|uninstall|status|run: the launchd job (" + DailyLabel + ", 09:00 local) that snapshots and writes the changelogs; install bootstraps it in gui/UID", Run: func(ctx context.Context, args []string) error {
			return t.DailyCommand(ctx, args)
		}},
	}
}
