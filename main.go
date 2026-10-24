// Command o-inventory is the family's derived inventory and changelog: it
// reads every o-* tool on PATH (and mtm) through its catalog answer, nabe's
// registry, o-proj's store and the hub links, and writes one JSON Lines
// snapshot per change under ~/hub/inventory. Nothing in it is hand-written:
// a wrong fact is fixed in the tool it came from.
//
//	o-inventory snapshot [--commit] [--quiet]     collect; write a snapshot when it differs
//	o-inventory diff [A] [B] [--jsonl] [--all]     changes between two snapshots
//	o-inventory changelog [--repo X] [--write-repos] [--commit] [--quiet]
//	o-inventory check                              drift; non-zero exit when anything is wrong
//	o-inventory hook install [REPO|--all]          the post-commit hook
//	o-inventory daily plist|install|uninstall|status|run   the launchd job
//	o-inventory repos                              the family repos (repos.json)
//	o-inventory NOUN list|show                     rows of the latest snapshot
//	o-inventory state                              every noun's rows, JSON Lines
//	o-inventory catalog                            the nouns and verbs, for a host (nabe)
//
// o abbreviates, so o inv ARGS and o inventory ARGS both run it.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

// Tool is one invocation's world: streams, environment and home. Every
// path the tool reads or writes is derived from Env and Home, so a test
// points it at a temporary tree.
type Tool struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Env            []string
	Home, Cwd      string
	Prog           string // "o inventory" under o, else "o-inventory"; messages only
	UID            int    // the launchd gui/UID domain of the daily job

	Now func() time.Time // time.Now unless a test sets the clock
}

func main() {
	home, err := os.UserHomeDir()
	if err == nil {
		home, err = filepath.Abs(home)
	}
	cwd, cwdErr := os.Getwd()
	if err == nil {
		err = cwdErr
	}
	prog := "o-inventory"
	if parent := os.Getenv(oden.EnvParent); parent != "" {
		prog = parent + " inventory"
	}
	if err != nil {
		os.Exit(oden.Report(os.Stderr, prog, err))
	}
	t := &Tool{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Env: os.Environ(), Home: home, Cwd: cwd, Prog: prog, UID: os.Getuid(), Now: time.Now}
	os.Exit(oden.Report(os.Stderr, t.Prog, t.Main(context.Background(), os.Args[1:])))
}

// Main runs one command: an operation (snapshot, diff, ...), a noun of the
// runtime, catalog, or an oden builtin (help, commands, which).
func (t *Tool) Main(ctx context.Context, args []string) error {
	app := oden.New("o-inventory", args, oden.WithBuiltins(), oden.WithStdio(t.Stdin, t.Stdout, t.Stderr), oden.WithEnv(t.Env))
	sh := t.shell()
	for _, c := range t.commands() {
		app.Add(c)
	}
	mount(app, sh)
	return app.Run(ctx)
}

// mount makes each of the runtime's own nouns a command running the shell,
// plus catalog and state (inventory state). The framework's nouns stay out
// of the command words; they remain in the catalog, where a host skips them.
func mount(app *oden.App, sh *ocli.Shell) {
	for _, noun := range shellNouns {
		var verbs []string
		for _, op := range sh.Runtime.Catalog() {
			if op.Noun == noun {
				verbs = append(verbs, op.Verb)
			}
		}
		app.Add(oden.Command{Name: noun, Summary: strings.Join(verbs, " "), Source: "noun", Run: func(ctx context.Context, rest []string) error {
			return sh.Run(ctx, append([]string{noun}, rest...))
		}})
	}
	app.Add(oden.Command{Name: "state", Summary: "every noun's rows of the latest snapshot as JSON Lines (inventory state)", Source: "noun", Run: func(ctx context.Context, rest []string) error {
		return sh.Run(ctx, append([]string{"inventory", "state"}, rest...))
	}})
	app.Add(oden.Command{Name: "catalog", Summary: "print the nouns, verbs, bindings and pages as JSON", Source: "cli", Run: func(context.Context, []string) error {
		data, err := ocli.Catalog(sh.Runtime, sh.Bin, sh.Version)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(sh.Stdout, "%s\n", data)
		return err
	}})
}

// getenv is a variable of t.Env, "" when unset.
func (t *Tool) getenv(key string) string {
	for i := len(t.Env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(t.Env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}

func (t *Tool) now() time.Time {
	if t.Now == nil {
		return time.Now()
	}
	return t.Now()
}

// Hub is the hub repository: $O_INV_HUB, else ~/hub.
func (t *Tool) Hub() string {
	if v := t.getenv("O_INV_HUB"); v != "" {
		return v
	}
	return filepath.Join(t.Home, "hub")
}

// InventoryDir holds the snapshots.
func (t *Tool) InventoryDir() string { return filepath.Join(t.Hub(), "inventory") }

// RegistryDir is nabe's registry: $XDG_DATA_HOME/nabe/catalogs, else
// ~/.local/share/nabe/catalogs.
func (t *Tool) RegistryDir() string {
	base := t.getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(t.Home, ".local", "share")
	}
	return filepath.Join(base, "nabe", "catalogs")
}

// ConfigDir is $XDG_CONFIG_HOME/o-inventory, else ~/.config/o-inventory.
func (t *Tool) ConfigDir() string {
	base := t.getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(t.Home, ".config")
	}
	return filepath.Join(base, "o-inventory")
}

// tilde is path with the home directory spelled ~.
func (t *Tool) tilde(path string) string {
	if path == t.Home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, t.Home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return path
}

// untilde is the absolute path a tilde path names.
func (t *Tool) untilde(path string) string {
	if path == "~" {
		return t.Home
	}
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		return filepath.Join(t.Home, rest)
	}
	return path
}

// say prints a progress line unless quiet.
func say(w io.Writer, quiet bool, format string, args ...any) {
	if !quiet {
		fmt.Fprintf(w, format+"\n", args...)
	}
}
