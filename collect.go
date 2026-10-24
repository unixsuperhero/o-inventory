package main

import (
	"bufio"
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// CatalogTimeout bounds each BIN catalog (and o-proj list, o-story state).
const CatalogTimeout = 20 * time.Second

// Framework is the tool name the framework's nouns, verbs and bindings are
// recorded under, once, instead of once per tool.
const Framework = "ochazuke"

// pathTool is an executable found on PATH.
type pathTool struct {
	Name     string // the bin name: o-ai, mtm
	Path     string // where PATH found it first
	Resolved string // Path with symlinks resolved
}

// pathTools are the o-* executables (and mtm) on PATH, the first of each
// name, sorted by name.
func (t *Tool) pathTools() []pathTool {
	seen := map[string]bool{}
	var out []pathTool
	for _, dir := range filepath.SplitList(t.getenv("PATH")) {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if seen[name] || !(strings.HasPrefix(name, "o-") || name == "mtm") {
				continue
			}
			full := filepath.Join(dir, name)
			info, err := os.Stat(full)
			if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
				continue
			}
			seen[name] = true
			resolved, err := filepath.EvalSymlinks(full)
			if err != nil {
				resolved = full
			}
			out = append(out, pathTool{Name: name, Path: full, Resolved: resolved})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// run is BIN ARGS with the tool's environment plus MTM_SERVER=default (so
// mtm never starts a daemon), bounded by CatalogTimeout; its stdout.
func (t *Tool) run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, CatalogTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(append([]string{}, t.Env...), "MTM_SERVER=default")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg != "" {
			return nil, fmt.Errorf("%s %s: %w: %s", filepath.Base(bin), strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("%s %s: %w", filepath.Base(bin), strings.Join(args, " "), err)
	}
	return out, nil
}

// buildFacts are what a Go binary's build info says: its module's repo name
// (the module path's last element), module and commit.
func buildFacts(bin string) (repo, module, commit string) {
	info, err := buildinfo.ReadFile(bin)
	if err != nil {
		return "", "", ""
	}
	module = info.Main.Path
	repo = path.Base(module)
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && len(s.Value) >= 7 {
			commit = s.Value[:7]
		}
	}
	return repo, module, commit
}

// Entry is one nabe registry file (nabe registry.go).
type Entry struct {
	Bin          string           `json:"bin"`
	RegisteredAt string           `json:"registeredAt"`
	Order        *int             `json:"order,omitempty"`
	RefreshEvery string           `json:"refreshEvery,omitempty"`
	Catalog      *ocli.CatalogDoc `json:"catalog"`
}

// registry is nabe's registry by extension name: every NAME.json, skipping
// dot files (its .tmp writes).
func (t *Tool) registry() (map[string]Entry, []string, error) {
	dir := t.RegistryDir()
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, nil, err
	}
	out := map[string]Entry{}
	var names []string
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, ".") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		var e Entry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f, err)
		}
		name := strings.TrimSuffix(base, ".json")
		out[name] = e
		names = append(names, name)
	}
	sort.Strings(names)
	return out, names, nil
}

// Project is a row of o-proj list (matomeru internal/work Project).
type Project struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Repo   string `json:"repo"`
	Origin string `json:"origin"`
	Trees  []struct {
		Kind   string `json:"kind"`
		Path   string `json:"path"`
		Branch string `json:"branch"`
	} `json:"trees"`
}

// hubLinks are the hub's o/ symlinks: name -> absolute target.
func (t *Tool) hubLinks() map[string]string {
	dir := filepath.Join(t.Hub(), "o")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		target, err := os.Readlink(full)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(dir, target)
		}
		out[e.Name()] = filepath.Clean(target)
	}
	return out
}

// Collection is one collection's rows and what could not be read.
type Collection struct {
	Rows     []Row
	Warnings []string
}

// Collect reads every source into rows (not yet sorted): tools on PATH and
// their catalogs, nabe's registry, o-proj's projects and the hub links, and
// o-story's stories and mode edges when it answers a catalog with a state.
func (t *Tool) Collect(ctx context.Context) Collection {
	var c Collection
	warn := func(format string, args ...any) { c.Warnings = append(c.Warnings, fmt.Sprintf(format, args...)) }
	ids := map[string]bool{}
	add := func(r Row) bool {
		if ids[r.ID()] {
			return false
		}
		ids[r.ID()] = true
		c.Rows = append(c.Rows, r)
		return true
	}

	reg, regNames, err := t.registry()
	if err != nil {
		warn("nabe registry: %v", err)
	}
	// mounted are the nouns a host carries for an extension (NAME-NOUN):
	// recorded once, under the extension's own tool.
	mounted := map[string]bool{}
	for name, e := range reg {
		if e.Catalog != nil {
			for _, n := range e.Catalog.Nouns {
				mounted[name+"-"+n.Name] = true
			}
		}
	}

	tools := t.pathTools()
	byResolved := map[string]string{}
	docs := map[string]*ocli.CatalogDoc{}
	bins := map[string]string{}
	for _, pt := range tools {
		bins[pt.Name] = pt.Path
		row := newRow("tool", pt.Name)
		row["name"] = pt.Name
		row["bin"] = t.tilde(pt.Path)
		repo, module, commit := buildFacts(pt.Resolved)
		row.set("repo", repo).set("module", module).set("commit", commit)
		if info, err := os.Stat(pt.Resolved); err == nil {
			row["binMtime"] = info.ModTime().UTC().Format(time.RFC3339)
		}
		if first, ok := byResolved[pt.Resolved]; ok {
			row["alias"] = first
			row["catalog"] = nil
			add(row)
			continue
		}
		byResolved[pt.Resolved] = pt.Name
		out, err := t.run(ctx, pt.Path, "catalog")
		var doc ocli.CatalogDoc
		if err == nil {
			err = json.Unmarshal(out, &doc)
		}
		if err != nil {
			warn("%s: no catalog: %v", pt.Name, err)
			row["catalog"] = nil
			add(row)
			continue
		}
		row["catalog"] = true
		row.set("version", doc.Version).set("state", doc.State)
		add(row)
		docs[pt.Name] = &doc
	}
	for _, pt := range tools {
		if doc := docs[pt.Name]; doc != nil {
			catalogRows(pt.Name, doc, mounted, add, func(id string) bool { return ids[id] })
		}
	}

	for _, name := range regNames {
		e := reg[name]
		row := newRow("host", "nabe/"+name)
		row["name"] = name
		row["bin"] = t.tilde(e.Bin)
		if e.Order != nil {
			row["order"] = *e.Order
		} else {
			row["order"] = 0
		}
		row.set("refreshEvery", e.RefreshEvery).set("registeredAt", e.RegisteredAt)
		if e.Catalog != nil {
			row.set("catalogName", e.Catalog.Name).set("catalogVersion", e.Catalog.Version)
		}
		add(row)
	}

	links := t.hubLinks()
	linked := map[string]bool{}
	if bin := bins["o-proj"]; bin != "" {
		out, err := t.run(ctx, bin, "list")
		var projects []Project
		if err == nil {
			err = json.Unmarshal(out, &projects)
		}
		if err != nil {
			warn("o-proj list: %v", err)
		}
		for _, p := range projects {
			row := newRow("project", p.Name)
			row["name"] = p.Name
			row["registered"] = true
			row.set("status", p.Status).set("origin", p.Origin)
			var trees []string
			for _, tr := range p.Trees {
				trees = append(trees, t.tilde(tr.Path))
			}
			if len(trees) > 0 {
				row["path"] = trees[0]
			}
			row.set("trees", trees)
			for name, target := range links {
				if name == p.Name || (len(p.Trees) > 0 && target == p.Trees[0].Path) {
					row["hubLink"] = true
					linked[name] = true
				}
			}
			if !add(row) {
				warn("project %q is registered twice; the first is recorded", p.Name)
			}
		}
	}
	for name, target := range links {
		if !linked[name] {
			row := newRow("project", name)
			row["name"] = name
			row["path"] = t.tilde(target)
			row["hubLink"] = true
			add(row)
		}
	}

	if doc := docs["o-story"]; doc != nil && doc.State != "" {
		noun, verb, _ := strings.Cut(doc.State, ".")
		out, err := t.run(ctx, bins["o-story"], noun, verb)
		if err != nil {
			warn("o-story state: %v", err)
		}
		storyRows(out, add)
	}
	return c
}

// catalogRows are a catalog's noun, verb, param, binding and page rows.
// A noun with no application verb is the framework's (cli.Mount's rule:
// it is not mounted as a command): it and its verbs and bindings go under
// Framework, first tool wins. A host's mounted NAME-NOUN nouns are skipped.
func catalogRows(tool string, doc *ocli.CatalogDoc, mounted map[string]bool, add func(Row) bool, has func(string) bool) {
	own := map[string]bool{}
	for _, n := range doc.Nouns {
		for _, v := range n.Verbs {
			if v.Kind == ochazuke.KindApplication {
				own[n.Name] = true
			}
		}
	}
	owner := func(noun string) string {
		if own[noun] {
			return tool
		}
		return Framework
	}
	for _, n := range doc.Nouns {
		if mounted[n.Name] {
			continue
		}
		o := owner(n.Name)
		row := newRow("noun", o+"/"+n.Name)
		row["tool"], row["noun"] = o, n.Name
		row.set("framework", o == Framework).set("pin", n.Pin)
		if !add(row) {
			continue // the framework's, recorded from an earlier tool
		}
		for _, v := range n.Verbs {
			vr := newRow("verb", o+"/"+n.Name+"."+v.Name)
			vr["tool"], vr["noun"], vr["verb"] = o, n.Name, v.Name
			vr.set("verbKind", v.Kind).set("result", v.Result).set("confirm", v.Confirm).
				set("repeatable", v.Repeatable).set("rows", v.Rows).set("quiet", v.Quiet).
				set("positional", v.Positional).set("description", v.Description)
			var params []string
			for name := range v.Params {
				params = append(params, name)
			}
			sort.Strings(params)
			vr.set("params", params)
			add(vr)
			for _, name := range params {
				p := v.Params[name]
				pr := newRow("param", o+"/"+n.Name+"."+v.Name+"/"+name)
				pr["tool"], pr["noun"], pr["verb"], pr["param"] = o, n.Name, v.Name, name
				pr.set("type", p.Type).set("required", p.Required).set("enum", p.Enum).
					set("source", p.Source).set("free", p.Free).set("description", p.Description)
				add(pr)
			}
		}
	}
	seen := map[string]int{}
	for _, b := range doc.Bindings {
		if mounted[b.Noun] {
			continue
		}
		o := owner(b.Noun)
		keys := b.Keys
		if keys == "" {
			keys = "~" + b.Pattern
		}
		id := o + "/" + b.Mode + "/" + b.Noun + "/" + keys
		if b.Instance != "" {
			id += "@" + b.Instance
		}
		if o == Framework && seen[id] == 0 && has("binding:"+id) {
			continue
		}
		seen[id]++
		if seen[id] > 1 {
			id = fmt.Sprintf("%s#%d", id, seen[id])
		}
		row := newRow("binding", id)
		row["tool"], row["mode"], row["noun"] = o, b.Mode, b.Noun
		row.set("keys", b.Keys).set("pattern", b.Pattern).set("instance", b.Instance).
			set("request", requestKey(b.Request)).set("when", b.When).set("unbind", b.Unbind).set("replay", b.Replay)
		if len(b.Request.Args) > 0 {
			if args, err := json.Marshal(b.Request.Args); err == nil {
				row["args"] = string(args)
			}
		}
		add(row)
	}
	for i, p := range doc.Pages {
		row := newRow("page", tool+"/"+p.ID)
		row["tool"], row["page"] = tool, p.ID
		row.set("title", p.Title)
		row["order"] = i
		add(row)
	}
}

func requestKey(q ochazuke.Request) string {
	if q.Noun == "" {
		return q.Verb
	}
	return q.Noun + "." + q.Verb
}

// storyRows are o-story's state lines tagged story or edge.
func storyRows(out []byte, add func(Row) bool) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var line ocli.Row
		if json.Unmarshal(sc.Bytes(), &line) != nil || (line.Noun != "story" && line.Noun != "edge") {
			continue
		}
		path := strings.TrimPrefix(line.ID, line.Noun+"/")
		row := newRow(line.Noun, path)
		row.set("label", line.Label)
		if data, ok := line.Data.(map[string]any); ok {
			for _, k := range []string{"skip", "area", "page", "from", "to", "via"} {
				row.set(k, data[k])
			}
		}
		add(row)
	}
}
