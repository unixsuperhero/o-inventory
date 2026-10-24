package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

// Overview is the latest snapshot as one tree: the libraries and who
// builds on them, the roots (o and what it runs, then every other root:
// true tool), the tools, the nabe hosts and the projects.
type Overview struct {
	Libs     []OverviewLib     `json:"libs"`
	Root     []OverviewRoot    `json:"root"`
	Tools    []OverviewTool    `json:"tools"`
	Hosts    []OverviewHost    `json:"hosts"`
	Projects []OverviewProject `json:"projects"`
}

// OverviewLib is a lib and its consumers (tools and libs on it by go.mod,
// and tools built from its repo).
type OverviewLib struct {
	Name    string   `json:"name"`
	Commit  string   `json:"commit,omitempty"`
	Version string   `json:"version,omitempty"`
	Dirty   bool     `json:"dirty"`
	Deps    []string `json:"deps"`
}

// OverviewRoot is a root: o with the commands it runs (o-WORD tools, an
// alias as "tui (o-tui → o-nabe)"), or another root (mtm) with its source,
// counts and hosts.
type OverviewRoot struct {
	Name     string   `json:"name"`
	Bin      string   `json:"bin"`
	Commands []string `json:"commands"`
	Repo     string   `json:"repo,omitempty"`
	Commit   string   `json:"commit,omitempty"`
	Nouns    int      `json:"nouns"`
	Verbs    int      `json:"verbs"`
	Hosts    []string `json:"hosts"`
}

// OverviewTool is one tool on PATH but the roots.
type OverviewTool struct {
	Name      string   `json:"name"`
	Repo      string   `json:"repo,omitempty"`
	Commit    string   `json:"commit,omitempty"`
	Nouns     int      `json:"nouns"`
	Verbs     int      `json:"verbs"`
	Hosts     []string `json:"hosts"` // host/extension names mounting it
	Alias     string   `json:"alias,omitempty"`
	Catalog   bool     `json:"catalog"`
	Reachable bool     `json:"reachable"`
}

// OverviewHost is a host and its extensions in order.
type OverviewHost struct {
	Host       string              `json:"host"`
	Extensions []OverviewExtension `json:"extensions"`
}

// OverviewExtension is one registered extension of a host.
type OverviewExtension struct {
	Name         string `json:"name"`
	Order        int    `json:"order"`
	RefreshEvery string `json:"refreshEvery,omitempty"`
}

// OverviewProject is one project: o-proj's first, then the rest, by name.
type OverviewProject struct {
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Origin  string `json:"origin,omitempty"`
	HubLink bool   `json:"hubLink"`
	GitHub  bool   `json:"github"`
}

// Overview reads the latest snapshot into the tree.
func (t *Tool) Overview() (Overview, error) {
	rows, err := readRows(filepath.Join(t.InventoryDir(), Latest))
	if err != nil {
		return Overview{}, err
	}
	if len(rows) == 0 {
		return Overview{}, fmt.Errorf("no snapshot in %s (run snapshot)", t.tilde(t.InventoryDir()))
	}
	return t.overviewOf(rows), nil
}

func (t *Tool) overviewOf(rows []Row) Overview {
	ov := Overview{Libs: []OverviewLib{}, Root: []OverviewRoot{}, Tools: []OverviewTool{}, Hosts: []OverviewHost{}, Projects: []OverviewProject{}}
	byKind := map[string][]Row{}
	for _, r := range rows {
		byKind[r.Kind()] = append(byKind[r.Kind()], r)
	}
	for _, kind := range Kinds {
		sortRows(byKind[kind])
	}
	deps := map[string]map[string]bool{}
	addDep := func(lib, who string) {
		if deps[lib] == nil {
			deps[lib] = map[string]bool{}
		}
		deps[lib][who] = true
	}
	for _, d := range byKind["dep"] {
		addDep(d.Str("lib"), d.Str("tool"))
	}
	isLib := map[string]bool{}
	for _, l := range byKind["lib"] {
		isLib[l.Str("name")] = true
	}
	for _, r := range byKind["tool"] {
		if isLib[r.Str("repo")] && r.Str("alias") == "" {
			addDep(r.Str("repo"), r.Str("name"))
		}
	}
	for _, l := range byKind["lib"] {
		ol := OverviewLib{Name: l.Str("name"), Commit: l.Str("commit"), Version: l.Str("version"), Dirty: l["dirty"] == true, Deps: []string{}}
		for who := range deps[ol.Name] {
			ol.Deps = append(ol.Deps, who)
		}
		sort.Strings(ol.Deps)
		ov.Libs = append(ov.Libs, ol)
	}

	counts := map[string][2]int{}
	for _, kind := range []string{"noun", "verb"} {
		for _, r := range byKind[kind] {
			c := counts[r.Str("tool")]
			if kind == "noun" {
				c[0]++
			} else {
				c[1]++
			}
			counts[r.Str("tool")] = c
		}
	}
	hostsOf := map[string][]string{}
	hosts := map[string]*OverviewHost{}
	var hostNames []string
	for _, h := range byKind["host"] {
		host, _, _ := strings.Cut(strings.TrimPrefix(h.ID(), "host:"), "/")
		if hosts[host] == nil {
			hosts[host] = &OverviewHost{Host: host, Extensions: []OverviewExtension{}}
			hostNames = append(hostNames, host)
		}
		order := 0
		if n, ok := h["order"].(float64); ok {
			order = int(n)
		}
		hosts[host].Extensions = append(hosts[host].Extensions, OverviewExtension{Name: h.Str("name"), Order: order, RefreshEvery: h.Str("refreshEvery")})
		hostsOf[h.Str("bin")] = append(hostsOf[h.Str("bin")], host+" ("+h.Str("name")+")")
	}
	for _, host := range hostNames {
		h := hosts[host]
		sort.SliceStable(h.Extensions, func(i, j int) bool { return h.Extensions[i].Order < h.Extensions[j].Order })
		ov.Hosts = append(ov.Hosts, *h)
	}

	for _, r := range byKind["tool"] {
		name := r.Str("name")
		c := counts[name]
		if name == Root || r["root"] == true {
			root := OverviewRoot{Name: name, Bin: r.Str("bin"), Commands: []string{}, Hosts: hostsOf[r.Str("bin")]}
			if root.Hosts == nil {
				root.Hosts = []string{}
			}
			if name != Root {
				root.Repo, root.Commit, root.Nouns, root.Verbs = r.Str("repo"), r.Str("commit"), c[0], c[1]
				ov.Root = append(ov.Root, root)
				continue
			}
			for _, o := range byKind["tool"] {
				word, ok := strings.CutPrefix(o.Str("name"), Root+"-")
				if !ok || o["reachable"] == false || o["root"] == true {
					continue
				}
				if a := o.Str("alias"); a != "" {
					word += " (" + o.Str("name") + " → " + a + ")"
				}
				root.Commands = append(root.Commands, word)
			}
			ov.Root = append([]OverviewRoot{root}, ov.Root...)
			continue
		}
		ot := OverviewTool{Name: name, Repo: r.Str("repo"), Commit: r.Str("commit"), Nouns: c[0], Verbs: c[1], Hosts: hostsOf[r.Str("bin")],
			Alias: r.Str("alias"), Catalog: r["catalog"] == true, Reachable: r["reachable"] != false}
		if ot.Hosts == nil {
			ot.Hosts = []string{}
		}
		ov.Tools = append(ov.Tools, ot)
	}

	for _, p := range byKind["project"] {
		ov.Projects = append(ov.Projects, OverviewProject{Name: p.Str("name"), Path: p.Str("path"), Origin: p.Str("origin"), HubLink: p["hubLink"] == true, GitHub: p["github"] == true})
	}
	sort.SliceStable(ov.Projects, func(i, j int) bool {
		a, b := ov.Projects[i].Origin == "o-proj", ov.Projects[j].Origin == "o-proj"
		if a != b {
			return a
		}
		return ov.Projects[i].Name < ov.Projects[j].Name
	})
	return ov
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// Text is the tree as aligned lines, a count in each section's header.
func (ov Overview) Text() string {
	var b strings.Builder
	section := func(title string, n int, lines [][]string) {
		fmt.Fprintf(&b, "%s (%d)\n", title, n)
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		for _, cols := range lines {
			fmt.Fprintln(tw, "  "+strings.Join(cols, "\t"))
		}
		tw.Flush()
	}
	var lines [][]string
	for _, l := range ov.Libs {
		deps := strings.Join(l.Deps, ", ")
		if deps == "" {
			deps = "none"
		}
		lines = append(lines, []string{l.Name, l.Commit, "dirty: " + yesNo(l.Dirty), "deps: " + deps})
	}
	section("libs", len(ov.Libs), lines)
	lines = nil
	for _, r := range ov.Root {
		if r.Name == Root {
			lines = append(lines, []string{r.Name, r.Bin, "→ " + strings.Join(r.Commands, ", ")})
			continue
		}
		cols := []string{r.Name, r.Bin}
		if src := strings.TrimSpace(r.Repo + " " + r.Commit); src != "" {
			cols = append(cols, src)
		}
		cols = append(cols, plural(r.Nouns, "noun")+" "+plural(r.Verbs, "verb"))
		if len(r.Hosts) > 0 {
			cols = append(cols, "host: "+strings.Join(r.Hosts, ", "))
		}
		lines = append(lines, cols)
	}
	section("root", len(ov.Root), lines)
	lines = nil
	for _, tl := range ov.Tools {
		src := strings.TrimSpace(tl.Repo + " " + tl.Commit)
		var notes []string
		switch {
		case tl.Alias != "":
			notes = append(notes, "alias of "+tl.Alias)
		case !tl.Catalog:
			notes = append(notes, "no catalog")
		default:
			notes = append(notes, plural(tl.Nouns, "noun")+" "+plural(tl.Verbs, "verb"))
		}
		if len(tl.Hosts) > 0 {
			notes = append(notes, "host: "+strings.Join(tl.Hosts, ", "))
		}
		if !tl.Reachable {
			notes = append(notes, "not reachable through o")
		}
		lines = append(lines, append([]string{tl.Name, src}, notes...))
	}
	section("tools", len(ov.Tools), lines)
	lines = nil
	n := 0
	for _, h := range ov.Hosts {
		var exts []string
		for _, e := range h.Extensions {
			s := fmt.Sprintf("%s (order %d", e.Name, e.Order)
			if e.RefreshEvery != "" {
				s += ", every " + e.RefreshEvery
			}
			exts = append(exts, s+")")
		}
		n += len(h.Extensions)
		lines = append(lines, []string{h.Host + ": " + strings.Join(exts, ", ")})
	}
	section("hosts", n, lines)
	lines = nil
	for _, p := range ov.Projects {
		origin := "(no origin)"
		if p.Origin != "" {
			origin = "origin: " + p.Origin
		}
		lines = append(lines, []string{p.Name, p.Path, "hub/o: " + yesNo(p.HubLink), "github: " + yesNo(p.GitHub), origin})
	}
	section("projects", len(ov.Projects), lines)
	return b.String()
}

// OverviewCommand prints the overview as text, or the tree as JSON.
func (t *Tool) OverviewCommand(asJSON bool) error {
	ov, err := t.Overview()
	if err != nil {
		return err
	}
	if asJSON {
		data, err := json.MarshalIndent(ov, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(t.Stdout, "%s\n", data)
		return err
	}
	_, err = fmt.Fprint(t.Stdout, ov.Text())
	return err
}

// gitHubRemote reports whether the repository at dir has a github.com
// remote.
func gitHubRemote(dir string) bool {
	if _, err := os.Stat(dir); err != nil {
		return false
	}
	out, err := git(dir, "remote", "-v")
	return err == nil && strings.Contains(out, "github.com")
}
