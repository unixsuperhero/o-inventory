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

// pathTools are the root o, the o-* executables and mtm on PATH, the
// first of each name, sorted by name.
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
			if seen[name] || !(strings.HasPrefix(name, "o-") || name == "mtm" || name == Root) {
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

// Project is a row of o-proj list: a manifest entry (name, url, path with
// ~, branch, origin, dir); the id, status and tree fields are the older
// rows o-proj printed from mtm's store, still read when present.
type Project struct {
	Dir         string `json:"dir"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Repo        string `json:"repo"`
	Origin      string `json:"origin"`
	DefaultTree string `json:"default_tree"`
	Trees       []struct {
		Kind   string `json:"kind"`
		Path   string `json:"path"`
		Branch string `json:"branch"`
	} `json:"trees"`
}

// tree is the directory o-proj links from the hub: the manifest row's dir,
// else (older rows) the default tree, else the first.
func (p Project) tree() string {
	if p.Dir != "" {
		return p.Dir
	}
	if p.DefaultTree != "" {
		return p.DefaultTree
	}
	if len(p.Trees) > 0 {
		return p.Trees[0].Path
	}
	return ""
}

// HubLinkDir is the directory o-proj links projects from (o-proj
// DefaultHub): $O_PROJ_HUB, else ~/hub/o.
func (t *Tool) HubLinkDir() string {
	if v := t.getenv("O_PROJ_HUB"); v != "" {
		return v
	}
	return filepath.Join(t.Home, "hub", "o")
}

// sameDir reports whether a and b name the same directory once symlinks
// are resolved; a path that does not resolve matches nothing.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	rb, err := filepath.EvalSymlinks(b)
	return err == nil && ra == rb
}

// hubLinks are the hub's o/ symlinks: name -> absolute target.
func (t *Tool) hubLinks() map[string]string {
	dir := t.HubLinkDir()
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
	Catalogs map[string]*ocli.CatalogDoc // by tool name, for check
}

// Collect reads every source into rows (not yet sorted): tools on PATH and
// their catalogs, o's roster (reachable, shadow rows), the libraries and
// their consumers (lib, dep rows), nabe's registry, o-proj's projects and the hub links,
// o-story's stories and mode edges when it answers a catalog with a state,
// and mtm's store tables when its catalog has store tables.
func (t *Tool) Collect(ctx context.Context) Collection {
	c := Collection{Catalogs: map[string]*ocli.CatalogDoc{}}
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
	toolRows := map[string]Row{}
	var toolList []Row
	for _, pt := range tools {
		bins[pt.Name] = pt.Path
		row := newRow("tool", pt.Name)
		toolRows[pt.Name] = row
		toolList = append(toolList, row)
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
		c.Catalogs[pt.Name] = &doc
	}
	for _, pt := range tools {
		if doc := docs[pt.Name]; doc != nil {
			catalogRows(pt.Name, doc, mounted, add, func(id string) bool { return ids[id] })
		}
	}
	// A root (its catalog's command noun, or repos.json's roots) is
	// root: true; o's command rows are the roster: a tool o does not run
	// and is no root is reachable: false; a claim an o command shadows is a
	// shadow row.
	knownRepos, _ := t.knownRepos() // libRows warns on an error
	markRoots(knownRepos, docs, toolRows)
	t.markReachable(t.roster(ctx, bins[Root], docs[Root], warn), toolRows, add)
	t.libRows(toolList, add, warn)

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

	// Projects are the o-proj store's rows only; a hub link counts for the
	// project it is named after when it resolves to that project's tree.
	links := t.hubLinks()
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
			if tree := p.tree(); tree != "" {
				row["path"] = t.tilde(tree)
				row.set("github", gitHubRemote(tree))
			}
			row.set("trees", trees)
			if target, ok := links[p.Name]; ok && p.tree() != "" && sameDir(target, p.tree()) {
				row["hubLink"] = true
			}
			if !add(row) {
				warn("project %q is registered twice; the first is recorded", p.Name)
			}
		}
	}

	if doc := docs["o-story"]; doc != nil && doc.State != "" {
		noun, verb := ochazuke.SplitKey(doc.State)
		out, err := t.run(ctx, bins["o-story"], noun, verb)
		if err != nil {
			warn("o-story state: %v", err)
		}
		skip := func(id string) (string, error) { return t.storySkip(ctx, bins["o-story"], id) }
		storyRows(out, skip, add, warn)
	}

	// Store tables are mtm's read-only store tables answer, asked only
	// when its catalog has the verb (an mtm built before matomeru 9c0e73b
	// has none, and is skipped with a note).
	if doc := docs["mtm"]; doc != nil {
		if !hasVerb(doc, "store", "tables") {
			warn("mtm has no store tables verb (built before matomeru 9c0e73b; reinstall mtm): table rows skipped")
		} else if out, err := t.run(ctx, bins["mtm"], "store", "tables"); err != nil {
			warn("mtm store tables: %v", err)
		} else if err := tableRows("mtm", out, add); err != nil {
			warn("mtm store tables: %v", err)
		}
	}
	return c
}

// hasVerb reports whether doc has noun's verb.
func hasVerb(doc *ocli.CatalogDoc, noun, verb string) bool {
	for _, n := range doc.Nouns {
		if n.Name != noun {
			continue
		}
		for _, v := range n.Verbs {
			if v.Name == verb {
				return true
			}
		}
	}
	return false
}

// tableRows are a store tables answer's store-table rows (a JSON array of
// rows, or JSON Lines) as table rows, id table:NAME: {tool, name, owner,
// key, version, appendOnly}. The row count is not recorded: it changes
// with every write, and no snapshot should.
func tableRows(tool string, out []byte, add func(Row) bool) error {
	var lines []ocli.Row
	if err := json.Unmarshal(out, &lines); err != nil {
		lines = nil
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			var line ocli.Row
			if json.Unmarshal(sc.Bytes(), &line) == nil {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 && len(bytes.TrimSpace(out)) > 0 {
			return fmt.Errorf("not store-table rows: %w", err)
		}
	}
	for _, line := range lines {
		if line.Noun != "store-table" {
			continue
		}
		data, _ := line.Data.(map[string]any)
		name, _ := data["name"].(string)
		if name == "" {
			name = line.ID
		}
		row := newRow("table", name)
		row["tool"], row["name"] = tool, name
		row.set("owner", data["owner"]).set("key", data["key"]).set("appendOnly", data["append_only"])
		if v, ok := data["version"].(float64); ok {
			row["version"] = int(v)
		}
		add(row)
	}
	return nil
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
	for _, b := range sortedBindings(doc.Bindings) {
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

// storyRows are o-story's state lines tagged story or edge. A story row is
// {area, name, file, skip}: file is the story's file under o-story's
// stories directory (its id is AREA/STEM, the file's area directory and
// name), skip its "gap: ..." reason; the state line says only whether it
// is skipped, so a skipped story's reason is read with skip (story show).
func storyRows(out []byte, skip func(id string) (string, error), add func(Row) bool, warn func(string, ...any)) {
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
		data, _ := line.Data.(map[string]any)
		for _, k := range []string{"skip", "area", "page", "from", "to", "via", "file"} {
			row.set(k, data[k])
		}
		if line.Noun == "story" {
			row.set("name", line.Label)
			if row.Str("file") == "" {
				row["file"] = "stories/" + path + ".json"
			}
			if data["skipped"] == true && row.Str("skip") == "" {
				reason, err := skip(line.ID)
				if err != nil || reason == "" {
					warn("o-story story %s is skipped, reason unread: %v", line.ID, err)
					reason = "skipped"
				}
				row["skip"] = reason
			}
		}
		add(row)
	}
}

// storySkip is a story's skip reason, from o-story story show --target ID.
func (t *Tool) storySkip(ctx context.Context, bin, id string) (string, error) {
	out, err := t.run(ctx, bin, "story", "show", "--target", id)
	if err != nil {
		return "", err
	}
	var s struct {
		Skip string `json:"skip"`
	}
	if err := json.Unmarshal(out, &s); err != nil {
		return "", fmt.Errorf("story show: %w", err)
	}
	return s.Skip, nil
}

// sortedBindings are bindings in the order of their JSON: a catalog's rows
// within a mode are not in a stable order, and a duplicate key's #N id
// must not depend on it.
func sortedBindings(in []ochazuke.Binding) []ochazuke.Binding {
	type keyed struct {
		key string
		b   ochazuke.Binding
	}
	ks := make([]keyed, len(in))
	for i, b := range in {
		j, _ := json.Marshal(b)
		ks[i] = keyed{string(j), b}
	}
	sort.SliceStable(ks, func(i, j int) bool { return ks[i].key < ks[j].key })
	out := make([]ochazuke.Binding, len(in))
	for i, k := range ks {
		out[i] = k.b
	}
	return out
}
