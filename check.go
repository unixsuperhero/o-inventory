package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/unixsuperhero/oden"
)

// Problem is one drift finding: {kind, subject, detail}.
type Problem struct {
	Kind    string `json:"kind"`    // no-catalog, stale-catalog, missing-bin, missing-tree, missing-repo, no-hook
	Subject string `json:"subject"` // the row id or repo it is about
	Detail  string `json:"detail,omitempty"`
}

// Problems are the drift between the sources now: tools on PATH without a
// catalog; nabe hosts whose catalog is older than their bin (another
// version, or registered before the bin was built); hosts whose bin is
// missing; projects whose tree is missing; repos that are missing or lack
// the post-commit hook.
func (t *Tool) Problems(ctx context.Context) ([]Problem, error) {
	c := t.Collect(ctx)
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
	}
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
			if tool != nil && tool["catalog"] == true && tool.Str("version") != r.Str("catalogVersion") {
				add("stale-catalog", r.ID(), "registered catalog %s; %s answers %s", orNone(r.Str("catalogVersion")), tool.Str("name"), tool.Str("version"))
				continue
			}
			registered, err1 := time.Parse(time.RFC3339, r.Str("registeredAt"))
			if info, err2 := os.Stat(resolved); err1 == nil && err2 == nil && info.ModTime().After(registered) {
				add("stale-catalog", r.ID(), "registered %s; %s built %s", registered.UTC().Format(time.RFC3339), r.Str("bin"), info.ModTime().UTC().Format(time.RFC3339))
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
