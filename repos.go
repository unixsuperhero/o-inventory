package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Repo is one family repository: the changelog's per-repo files, the
// commits it quotes and the hooks check and hook install --all read.
type Repo struct {
	Name string `json:"name"`
	Path string `json:"path"` // ~ for the home directory
}

// ReposPath is the repos config: CONFIG/o-inventory/repos.json.
func (t *Tool) ReposPath() string { return filepath.Join(t.ConfigDir(), "repos.json") }

// seededFrameworkRepos are the repos the family is built on, seeded beside
// the hub's o/ links (which list only the projects o-proj made).
var seededFrameworkRepos = []string{"ochazuke", "oden", "matomeru"}

// Repos is the repos config, written on first use from the hub's o/ links
// whose target is a repository of its own, plus ~/proj/{ochazuke, oden,
// matomeru}; seeded reports that this call wrote it.
func (t *Tool) Repos() (repos []Repo, seeded bool, err error) {
	repos, exists, err := t.readRepos()
	if err != nil || exists {
		return repos, false, err
	}
	repos = t.seedRepos()
	if err := t.saveRepos(repos); err != nil {
		return nil, false, err
	}
	return repos, true, nil
}

// knownRepos is the repos config, or the seed it would be written with,
// without writing it (collection reads it; only Repos seeds the file).
func (t *Tool) knownRepos() ([]Repo, error) {
	repos, exists, err := t.readRepos()
	if err != nil || exists {
		return repos, err
	}
	return t.seedRepos(), nil
}

// readRepos is repos.json; exists is false when there is none.
func (t *Tool) readRepos() (repos []Repo, exists bool, err error) {
	data, err := os.ReadFile(t.ReposPath())
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(data, &repos); err != nil {
		return nil, true, fmt.Errorf("%s: %w", t.tilde(t.ReposPath()), err)
	}
	return repos, true, nil
}

// seedRepos are the hub's o/ links that are repositories of their own and
// ~/proj/{ochazuke, oden, matomeru}, by name.
func (t *Tool) seedRepos() []Repo {
	var repos []Repo
	byPath := map[string]Repo{}
	for name, target := range t.hubLinks() {
		if isRepoRoot(target) {
			byPath[target] = Repo{Name: name, Path: t.tilde(target)}
		}
	}
	for _, name := range seededFrameworkRepos {
		dir := filepath.Join(t.Home, "proj", name)
		if _, ok := byPath[dir]; !ok {
			byPath[dir] = Repo{Name: name, Path: t.tilde(dir)}
		}
	}
	for _, r := range byPath {
		repos = append(repos, r)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	return repos
}

// saveRepos writes the repos config, sorted by name.
func (t *Tool) saveRepos(repos []Repo) error {
	repos = append([]Repo(nil), repos...)
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	out, err := json.MarshalIndent(repos, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(t.ReposPath(), append(out, '\n'))
}

// isRepoRoot reports whether dir is the top of a git repository of its
// own (a .git directory or worktree file), not a directory inside one.
func isRepoRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// ReposCommand prints the repos config.
func (t *Tool) ReposCommand() error {
	repos, seeded, err := t.Repos()
	if err != nil {
		return err
	}
	if seeded {
		fmt.Fprintf(t.Stderr, "seeded %s\n", t.tilde(t.ReposPath()))
	}
	out, err := json.MarshalIndent(repos, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(t.Stdout, "%s\n", out)
	return err
}
