package main

import (
	"context"
	"encoding/json"
	"fmt"
	ocli "github.com/unixsuperhero/ochazuke/cli"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/unixsuperhero/oden"
)

// Problem is one drift finding: {kind, subject, detail}.
type Problem struct {
	Kind    string `json:"kind"`    // no-catalog, stale-catalog, stale-build, unreachable, missing-bin, missing-tree, missing-repo, no-hook
	Subject string `json:"subject"` // the row id or repo it is about
	Detail  string `json:"detail,omitempty"`
}

// Problems are the drift between the sources now: tools on PATH without a
// catalog; nabe hosts whose catalog is older than their bin (another
// version, or registered before the bin was built); hosts whose bin is
// missing; projects whose tree is missing; repos that are missing or lack
// the post-commit hook; tools built before the last commit of a lib they
// build from (stale-build); tools o does not run (unreachable).
func (t *Tool) Problems(ctx context.Context) ([]Problem, error) {
	c := t.Collect(ctx)
	reg, _, _ := t.registry()
	var out []Problem
	add := func(kind, subject, format string, args ...any) {
		out = append(out, Problem{Kind: kind, Subject: subject, Detail: fmt.Sprintf(format, args...)})
	}
	toolByBin := map[string]Row{}
	for _, r := range c.Rows {
		if r.Kind() != "tool" {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(t.untilde(r.Str("bin"))); err == nil {
			toolByBin[resolved] = r
		}
		if r["catalog"] != true && r.Str("alias") == "" {
			add("no-catalog", r.ID(), "%s catalog fails", r.Str("bin"))
		}
		if r["reachable"] == false {
			add("unreachable", r.ID(), "%s does not list %s (only o-* executables are o's commands)", Root, r.Str("name"))
		}
	}
	out = append(out, staleBuilds(c.Rows)...)
	for _, r := range c.Rows {
		switch r.Kind() {
		case "host":
			bin := t.untilde(r.Str("bin"))
			resolved, err := filepath.EvalSymlinks(bin)
			if err != nil {
				add("missing-bin", r.ID(), "%s is missing", r.Str("bin"))
				continue
			}
			tool := toolByBin[resolved]
			if tool == nil || tool["catalog"] != true {
				continue
			}
			if diff := catalogDrift(reg[r.Str("name")].Catalog, c.Catalogs[tool.Str("name")]); diff != "" {
				add("stale-catalog", r.ID(), "registered catalog differs from what %s answers now: %s (o-nabe extension register %s)", tool.Str("name"), diff, tool.Str("name"))
			}
		case "project":
			trees, _ := r["trees"].([]string)
			if len(trees) == 0 && r.Str("path") != "" {
				trees = []string{r.Str("path")}
			}
			for _, tree := range trees {
				if _, err := os.Stat(t.untilde(tree)); err != nil {
					add("missing-tree", r.ID(), "%s is missing", tree)
				}
			}
		}
	}
	repos, _, err := t.Repos()
	if err != nil {
		return nil, err
	}
	for _, repo := range repos {
		dir := t.untilde(repo.Path)
		switch {
		case !isRepoRoot(dir):
			add("missing-repo", "repo:"+repo.Name, "%s is not a repository", repo.Path)
		case !hookInstalled(dir):
			add("no-hook", "repo:"+repo.Name, "%s has no o-inventory post-commit hook (o inv hook install %s)", repo.Path, repo.Name)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Subject < out[j].Subject
	})
	return out, nil
}

// staleBuilds are the tools whose binary is older than the last commit of
// a lib they build from: the lib's own repo, or a go.mod replace onto its
// checkout (a require pins a version, which a lib commit does not change).
func staleBuilds(rows []Row) []Problem {
	libs := map[string]Row{}
	tools := map[string]Row{}
	for _, r := range rows {
		switch r.Kind() {
		case "lib":
			libs[r.Str("name")] = r
		case "tool":
			if r.Str("alias") == "" {
				tools[r.Str("name")] = r
			}
		}
	}
	uses := map[string]map[string]bool{} // tool -> libs
	use := func(tool, lib string) {
		if uses[tool] == nil {
			uses[tool] = map[string]bool{}
		}
		uses[tool][lib] = true
	}
	for name, r := range tools {
		if libs[r.Str("repo")] != nil {
			use(name, r.Str("repo"))
		}
	}
	for _, r := range rows {
		if r.Kind() == "dep" && r.Str("consumer") == "tool" && r.Str("mode") == "replace" {
			use(r.Str("tool"), r.Str("lib"))
		}
	}
	var out []Problem
	for name, libSet := range uses {
		built, err := time.Parse(time.RFC3339, tools[name].Str("binMtime"))
		if err != nil {
			continue
		}
		for lib := range libSet {
			l := libs[lib]
			committed, err := time.Parse(time.RFC3339, l.Str("commitTime"))
			if err != nil || !built.Before(committed) {
				continue
			}
			out = append(out, Problem{Kind: "stale-build", Subject: "tool:" + name,
				Detail: fmt.Sprintf("%s built %s, before %s %s (%s); rebuild it", tools[name].Str("bin"), built.UTC().Format(time.RFC3339), lib, l.Str("commit"), committed.UTC().Format(time.RFC3339))})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Subject != out[j].Subject {
			return out[i].Subject < out[j].Subject
		}
		return out[i].Detail < out[j].Detail
	})
	return out
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Check prints the problems, readable or as JSON Lines, and fails (exit 1)
// when there is any.
func (t *Tool) Check(ctx context.Context, jsonl bool) error {
	problems, err := t.Problems(ctx)
	if err != nil {
		return err
	}
	for _, p := range problems {
		if jsonl {
			b, _ := json.Marshal(p)
			fmt.Fprintf(t.Stdout, "%s\n", b)
		} else {
			fmt.Fprintf(t.Stdout, "%-13s %-24s %s\n", p.Kind, p.Subject, p.Detail)
		}
	}
	if len(problems) == 0 {
		if !jsonl {
			fmt.Fprintln(t.Stdout, "ok: no drift")
		}
		return nil
	}
	if !jsonl {
		fmt.Fprintln(t.Stderr, plural(len(problems), "problem"))
	}
	return &oden.ExitError{Code: 1}
}

// catalogDrift is how a registered catalog differs from the one its bin
// answers now, "" when they agree; the version and name are not compared
// (a rebuild with the same nouns, verbs, bindings and pages is current).
func catalogDrift(registered, now *ocli.CatalogDoc) string {
	if registered == nil || now == nil {
		return ""
	}
	var parts []string
	same := func(a, b any) bool {
		x, _ := json.Marshal(a)
		y, _ := json.Marshal(b)
		return string(x) == string(y)
	}
	if !same(registered.Nouns, now.Nouns) {
		parts = append(parts, fmt.Sprintf("nouns and verbs (%d nouns registered, %d now)", len(registered.Nouns), len(now.Nouns)))
	}
	if !same(sortedBindings(registered.Bindings), sortedBindings(now.Bindings)) {
		parts = append(parts, fmt.Sprintf("bindings (%d registered, %d now)", len(registered.Bindings), len(now.Bindings)))
	}
	if !same(registered.Pages, now.Pages) {
		parts = append(parts, "pages")
	}
	if registered.State != now.State {
		parts = append(parts, fmt.Sprintf("state %s; was %s", orNone(now.State), orNone(registered.State)))
	}
	return strings.Join(parts, ", ")
}
