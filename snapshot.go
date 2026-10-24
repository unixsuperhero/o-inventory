package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// StampLayout is a snapshot's name: its UTC time, sortable as text.
const StampLayout = "20060102T150405Z"

// Latest is the copy of the newest snapshot.
const Latest = "latest.jsonl"

// snapshots are the snapshot files' stamps, oldest first.
func (t *Tool) snapshots() ([]string, error) {
	files, err := filepath.Glob(filepath.Join(t.InventoryDir(), "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		stamp := strings.TrimSuffix(filepath.Base(f), ".jsonl")
		if _, err := time.Parse(StampLayout, stamp); err == nil {
			out = append(out, stamp)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (t *Tool) snapshotPath(stamp string) string {
	return filepath.Join(t.InventoryDir(), stamp+".jsonl")
}

// lock holds the inventory directory's lock until the returned func runs,
// so two hooks firing together write one snapshot after the other.
func (t *Tool) lock() (func(), error) {
	if err := os.MkdirAll(t.InventoryDir(), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(t.InventoryDir(), ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// SnapshotResult is what a snapshot wrote: the stamp and its changes
// against the previous one; Stamp is "" when nothing changed.
type SnapshotResult struct {
	Stamp   string
	Rows    int
	Changes []Change
}

// Snapshot collects every source and writes inventory/STAMP.jsonl and
// latest.jsonl, unless the rows equal latest.jsonl's in every compared
// field; commit then commits both in the hub.
func (t *Tool) Snapshot(ctx context.Context, commit, quiet bool) (SnapshotResult, error) {
	unlock, err := t.lock()
	if err != nil {
		return SnapshotResult{}, err
	}
	defer unlock()
	c := t.Collect(ctx)
	if !quiet {
		for _, w := range c.Warnings {
			fmt.Fprintln(t.Stderr, "warning:", w)
		}
	}
	rows := c.Rows
	sortRows(rows)
	latest := filepath.Join(t.InventoryDir(), Latest)
	prev, err := readRows(latest)
	if err != nil {
		return SnapshotResult{}, err
	}
	changes := Diff(prev, rows, false)
	_, statErr := os.Stat(latest)
	if len(changes) == 0 && statErr == nil {
		say(t.Stdout, quiet, "no change (%d rows, as %s)", len(rows), t.tilde(latest))
		return SnapshotResult{Rows: len(rows)}, nil
	}
	data, err := encodeRows(rows)
	if err != nil {
		return SnapshotResult{}, err
	}
	stamp := t.now().UTC().Format(StampLayout)
	if stamps, _ := t.snapshots(); len(stamps) > 0 && stamps[len(stamps)-1] >= stamp {
		last, _ := time.Parse(StampLayout, stamps[len(stamps)-1])
		stamp = last.Add(time.Second).Format(StampLayout) // one snapshot per second, in order
	}
	path := t.snapshotPath(stamp)
	if err := writeFile(path, data); err != nil {
		return SnapshotResult{}, err
	}
	if err := writeFile(latest, data); err != nil {
		return SnapshotResult{}, err
	}
	say(t.Stdout, quiet, "wrote %s: %d rows, %d changes", t.tilde(path), len(rows), len(changes))
	if commit {
		msg := fmt.Sprintf("inventory: %s (%d changes)", stamp, len(changes))
		if err := gitCommit(t.Hub(), msg, path, latest); err != nil {
			return SnapshotResult{}, err
		}
		say(t.Stdout, quiet, "committed in %s: %s", t.tilde(t.Hub()), msg)
	}
	return SnapshotResult{Stamp: stamp, Rows: len(rows), Changes: changes}, nil
}

// writeFile writes data atomically (a temp file renamed over path).
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		os.Chmod(f.Name(), info.Mode().Perm())
	} else {
		os.Chmod(f.Name(), 0o644)
	}
	return os.Rename(f.Name(), path)
}

// git runs git -C dir ARGS; its trimmed stdout.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitCommit commits paths (and only them) in repo dir with msg; nothing
// when they are unchanged.
func gitCommit(dir, msg string, paths ...string) error {
	rel := make([]string, len(paths))
	for i, p := range paths {
		r, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel[i] = r
	}
	if _, err := git(dir, append([]string{"add", "--"}, rel...)...); err != nil {
		return err
	}
	if _, err := git(dir, append([]string{"diff", "--cached", "--quiet", "--"}, rel...)...); err == nil {
		return nil // nothing staged for these paths
	}
	_, err := git(dir, append([]string{"commit", "-q", "-m", msg, "--"}, rel...)...)
	return err
}

// gitEnv is the process environment without the GIT_* variables a hook
// runs under (GIT_DIR, GIT_INDEX_FILE, ...), which would point git -C DIR
// back at the repository that fired the hook.
func gitEnv() []string {
	var out []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "GIT_AUTHOR_") || strings.HasPrefix(kv, "GIT_COMMITTER_") || strings.HasPrefix(kv, "GIT_CONFIG") {
			out = append(out, kv)
		}
	}
	return out
}
