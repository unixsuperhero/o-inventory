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
	}
}
