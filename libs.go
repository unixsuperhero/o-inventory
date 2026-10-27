package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"github.com/unixsuperhero/oden"
)

// Root is the family root bin: its command rows are the roster a tool is
// reachable through.
const Root = "o"

// frameworkLibs are always libraries; any other repos.json module with no
// tool on PATH built from it is one too.
var frameworkLibs = []string{"oden", "ochazuke"}

// rosterEntry is the record of one o command row (oden.CommandData).
type rosterEntry struct {
	Kind    string   `json:"kind"`
	Path    string   `json:"path"`
	Source  string   `json:"source"`
	Aliases []string `json:"aliases"`
	Shadows []struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Source  string `json:"source"`
		AliasOf string `json:"alias_of"`
	} `json:"shadows"`
	Prefix string `json:"prefix"`
}

// roster is o's state answer (o command state) by command name; nil when
// o answers none.
func (t *Tool) roster(ctx context.Context, bin string, doc *ocli.CatalogDoc, warn func(string, ...any)) map[string]rosterEntry {
	if doc == nil || doc.State == "" {
		return nil
	}
	noun, verb := ochazuke.SplitKey(doc.State)
	out, err := t.run(ctx, bin, noun, verb)
	if err != nil {
		warn("%s state: %v", Root, err)
		return nil
	}
	m := map[string]rosterEntry{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var line struct {
			Noun string      `json:"noun"`
			ID   string      `json:"id"`
			Data rosterEntry `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || !isCommandNoun(doc, line.Noun) || line.ID == "" {
			continue
		}
		m[line.ID] = line.Data
	}
	return m
}

// markRoots marks each tool that is a root of its own root: true: its
// catalog has oden's command noun (o, any plain oden app), or repos.json
// names it in a repo's roots (an ochazuke-backed oden app such as mtm).
func markRoots(repos []Repo, docs map[string]*ocli.CatalogDoc, tools map[string]Row) {
	named := map[string]bool{}
	for _, r := range repos {
		for _, name := range r.Roots {
			named[name] = true
		}
	}
	for name, r := range tools {
		if named[name] || hasNoun(docs[name], oden.CommandNoun) {
			r["root"] = true
		}
	}
}

// hasNoun reports whether doc has noun, an alias resolved
// (CatalogDoc.CanonicalNoun): oden's command is app.command in an
// ochazuke v0.16.0 catalog (cli.CommandNoun), command in a plain oden one.
func hasNoun(doc *ocli.CatalogDoc, noun string) bool {
	if doc == nil {
		return false
	}
	names := []string{doc.CanonicalNoun(noun)}
	if noun == oden.CommandNoun {
		names = append(names, ocli.CommandNoun)
	}
	for _, n := range doc.Nouns {
		if slices.Contains(names, n.Name) {
			return true
		}
	}
	return false
}

// isCommandNoun reports whether a state line's noun is oden's command
// noun: command (a plain oden app, or the alias) or app.command (an
// ochazuke v0.16.0 one, cli.CommandNoun).
func isCommandNoun(doc *ocli.CatalogDoc, noun string) bool {
	c := doc.CanonicalNoun(noun)
	return c == doc.CanonicalNoun(oden.CommandNoun) || c == ocli.CommandNoun
}

// markReachable marks each tool o does not run (no o command whose
// executable is the tool's bin) reachable: false, a root excepted (it is
// not expected to be o's command), and adds a shadow row per claim an o
// command shadows.
func (t *Tool) markReachable(roster map[string]rosterEntry, tools map[string]Row, add func(Row) bool) {
	if roster == nil {
		return
	}
	runs := map[string]bool{}
	for _, e := range roster {
		if e.Kind == "external" && e.Path != "" {
			runs[t.tilde(e.Path)] = true
		}
	}
	for name, r := range tools {
		if name != Root && r["root"] != true && !runs[r.Str("bin")] {
			r["reachable"] = false
		}
	}
	names := make([]string, 0, len(roster))
	for name := range roster {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := roster[name]
		winner := e.Path
		if winner == "" {
			winner = e.Source
		}
		for _, s := range e.Shadows {
			row := newRow("shadow", Root+"/"+name+"/"+t.tilde(s.Source))
			row["tool"], row["name"] = Root, name
			row.set("winner", t.tilde(winner)).set("source", t.tilde(s.Source)).set("claimKind", s.Kind).set("aliasOf", s.AliasOf)
			add(row)
		}
	}
}

// goMod is what a go.mod says: its module, required versions and replace
// targets by module path.
type goMod struct {
	Module   string
	Require  map[string]string
	Replaces map[string]string
}

// readGoMod parses dir/go.mod's module, require and replace lines (single
// and block forms); a missing file is nil.
func readGoMod(dir string) *goMod {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil
	}
	m := &goMod{Require: map[string]string{}, Replaces: map[string]string{}}
	block := ""
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == ")" {
			block = ""
			continue
		}
		if block == "" {
			switch {
			case f[0] == "module" && len(f) > 1:
				m.Module = strings.Trim(f[1], `"`)
				continue
			case (f[0] == "require" || f[0] == "replace") && len(f) == 2 && f[1] == "(":
				block = f[0]
				continue
			case f[0] == "require" || f[0] == "replace":
				block, f = f[0], f[1:]
				m.line(block, f)
				block = ""
				continue
			}
			continue
		}
		m.line(block, f)
	}
	return m
}

func (m *goMod) line(kind string, f []string) {
	switch kind {
	case "require":
		if len(f) >= 2 {
			m.Require[f[0]] = f[1]
		}
	case "replace":
		for i, w := range f {
			if w == "=>" && i+1 < len(f) {
				m.Replaces[f[0]] = f[i+1]
			}
		}
	}
}

// repoDir is a repo's directory: repos.json's, else ~/proj/NAME.
func (t *Tool) repoDir(repos []Repo, name string) string {
	for _, r := range repos {
		if r.Name == name {
			return t.untilde(r.Path)
		}
	}
	return filepath.Join(t.Home, "proj", name)
}

// libRows are the lib rows (oden, ochazuke, and each repos.json module no
// tool on PATH is built from) read from each repo, and the dep rows of
// every tool's and lib's go.mod on a lib.
func (t *Tool) libRows(tools []Row, add func(Row) bool, warn func(string, ...any)) {
	repos, err := t.knownRepos()
	if err != nil {
		warn("repos: %v", err)
	}
	built := map[string]bool{}
	for _, r := range tools {
		built[r.Str("repo")] = true
	}
	names := map[string]bool{}
	for _, n := range frameworkLibs {
		names[n] = true
	}
	for _, r := range repos {
		if !built[r.Name] && readGoMod(t.untilde(r.Path)) != nil {
			names[r.Name] = true
		}
	}
	var libs []string
	for n := range names {
		libs = append(libs, n)
	}
	sort.Strings(libs)
	byModule := map[string]string{} // module path -> lib name
	for _, name := range libs {
		dir := t.repoDir(repos, name)
		if !isRepoRoot(dir) {
			continue
		}
		row := newRow("lib", name)
		row["name"], row["path"] = name, t.tilde(dir)
		commit, _ := git(dir, "rev-parse", "--short=7", "HEAD")
		when, _ := git(dir, "log", "-1", "--format=%cI")
		version, _ := git(dir, "describe", "--tags", "--always")
		status, _ := git(dir, "status", "--porcelain")
		dirty := false
		for _, l := range strings.Split(status, "\n") {
			// CHANGELOG.md is o-inventory's own uncommitted output.
			if l != "" && l != "?? CHANGELOG.md" {
				dirty = true
			}
		}
		row["dirty"] = dirty
		row.set("commit", commit).set("commitTime", when).set("version", version)
		// The source commit is the last that touched Go sources or module
		// files; a docs-only commit does not make a build stale.
		if src, _ := git(dir, "log", "-1", "--format=%H%x09%ct", "--", "*.go", "go.mod", "go.sum"); src != "" {
			hash, ct, _ := strings.Cut(src, "\t")
			if secs, err := strconv.ParseInt(ct, 10, 64); err == nil && len(hash) >= 7 {
				row.set("sourceCommit", hash[:7]).set("sourceTime", time.Unix(secs, 0).UTC().Format(time.RFC3339))
			}
		}
		if m := readGoMod(dir); m != nil {
			row.set("goModule", m.Module)
			byModule[m.Module] = name
		}
		add(row)
	}
	consumer := func(name, kind, dir string) {
		m := readGoMod(dir)
		if m == nil {
			return
		}
		mods := make([]string, 0, len(byModule))
		for mod := range byModule {
			mods = append(mods, mod)
		}
		sort.Strings(mods)
		for _, mod := range mods {
			lib := byModule[mod]
			version, required := m.Require[mod]
			target, replaced := m.Replaces[mod]
			if lib == name || (!required && !replaced) {
				continue
			}
			row := newRow("dep", name+"/"+lib)
			row["tool"], row["lib"], row["consumer"] = name, lib, kind
			row["mode"] = "require"
			if replaced {
				row["mode"] = "replace"
				if filepath.IsAbs(target) || strings.HasPrefix(target, ".") {
					if !filepath.IsAbs(target) {
						target = filepath.Join(dir, target)
					}
					target = t.tilde(filepath.Clean(target))
				}
				row["replace"] = target
			}
			row.set("version", version)
			add(row)
		}
	}
	for _, r := range tools {
		if r.Str("alias") == "" && r.Str("repo") != "" {
			consumer(r.Str("name"), "tool", t.repoDir(repos, r.Str("repo")))
		}
	}
	for _, name := range libs {
		consumer(name, "lib", t.repoDir(repos, name))
	}
}
