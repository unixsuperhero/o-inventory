package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Hook markers delimit the block hook install owns in a post-commit hook.
const (
	hookBegin = "# o-inventory:begin"
	hookEnd   = "# o-inventory:end"
)

// hooksDir is a repository's hooks directory (core.hooksPath, worktrees
// and all, as git resolves it).
func hooksDir(repo string) (string, error) {
	dir, err := git(repo, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repo, dir)
	}
	return dir, nil
}

// hookInstalled reports whether repo's post-commit hook has the block.
func hookInstalled(repo string) bool {
	dir, err := hooksDir(repo)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, "post-commit"))
	return err == nil && strings.Contains(string(data), hookBegin)
}

// hookBlock is the post-commit block: a background snapshot when the
// commit touched a Go file, go.mod, a stories directory or a catalog
// golden, and o-inventory is on PATH.
const hookBlock = hookBegin + ` (o inv hook install): snapshot when a commit touches what a catalog is built from
if command -v o-inventory >/dev/null 2>&1 && git diff-tree --no-commit-id --name-only -r --root HEAD | grep -Eq '(\.go|(^|/)go\.mod|(^|/)catalog\.golden)$|(^|/)stories/'; then
  (o-inventory snapshot --commit --quiet >/dev/null 2>&1 &)
fi
` + hookEnd + "\n"

// InstallHook writes the block into repo's post-commit hook: a new hook
// (#!/bin/sh and the block), the block appended to an existing one, or the
// block replaced in place; it reports what it did (created, appended,
// updated, unchanged).
func InstallHook(repo string) (string, string, error) {
	if !isRepoRoot(repo) {
		return "", "", fmt.Errorf("%s is not a repository", repo)
	}
	dir, err := hooksDir(repo)
	if err != nil {
		return "", "", err
	}
	path := filepath.Join(dir, "post-commit")
	data, err := os.ReadFile(path)
	var next, what string
	switch {
	case os.IsNotExist(err):
		next, what = "#!/bin/sh\n"+hookBlock, "created"
	case err != nil:
		return "", path, err
	default:
		cur := string(data)
		begin, end := strings.Index(cur, hookBegin), strings.Index(cur, hookEnd)
		if begin >= 0 && end > begin {
			stop := end + len(hookEnd)
			if stop < len(cur) && cur[stop] == '\n' {
				stop++
			}
			next, what = cur[:begin]+hookBlock+cur[stop:], "updated"
			if next == cur {
				what = "unchanged"
			}
		} else {
			if cur != "" && !strings.HasSuffix(cur, "\n") {
				cur += "\n"
			}
			next, what = cur+hookBlock, "appended"
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", path, err
	}
	if what != "unchanged" {
		if err := writeFile(path, []byte(next)); err != nil {
			return "", path, err
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", path, err
	}
	if info.Mode()&0o111 != 0o111 {
		if err := os.Chmod(path, info.Mode()|0o755); err != nil {
			return "", path, err
		}
		if what == "unchanged" {
			what = "made executable"
		}
	}
	return what, path, nil
}

// HookCommand is hook install [REPO|--all]: REPO a name from repos.json or
// a directory, default the working directory's repository.
func (t *Tool) HookCommand(args []string) error {
	f, err := parseFlags("hook", args, []string{"all"}, nil)
	if err != nil {
		return err
	}
	if len(f.words) == 0 || f.words[0] != "install" || len(f.words) > 2 || (f.bools["all"] && len(f.words) > 1) {
		return fmt.Errorf("usage: %s hook install [REPO|--all]", t.Prog)
	}
	repos, _, err := t.Repos()
	if err != nil {
		return err
	}
	var targets []Repo
	switch {
	case f.bools["all"]:
		targets = repos
	case len(f.words) == 2:
		name := f.words[1]
		for _, r := range repos {
			if r.Name == name {
				targets = append(targets, r)
			}
		}
		if len(targets) == 0 {
			dir, err := filepath.Abs(t.untilde(name))
			if err != nil {
				return err
			}
			targets = append(targets, Repo{Name: filepath.Base(dir), Path: dir})
		}
	default:
		top, err := git(t.Cwd, "rev-parse", "--show-toplevel")
		if err != nil {
			return err
		}
		targets = append(targets, Repo{Name: filepath.Base(top), Path: top})
	}
	var failed []string
	for _, r := range targets {
		what, path, err := InstallHook(t.untilde(r.Path))
		if err != nil {
			fmt.Fprintf(t.Stderr, "%s: %v\n", r.Name, err)
			failed = append(failed, r.Name)
			continue
		}
		fmt.Fprintf(t.Stdout, "%s: %s %s\n", r.Name, what, t.tilde(path))
	}
	if len(failed) > 0 {
		return fmt.Errorf("hook install failed in %s", strings.Join(failed, ", "))
	}
	return nil
}
