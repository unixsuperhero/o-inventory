package main

import (
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
