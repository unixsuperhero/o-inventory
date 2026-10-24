package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// commitAll commits everything in dir (git init on first use) at when.
func commitAll(t *testing.T, dir, subject, when string) {
	t.Helper()
	run := func(env []string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(gitEnv(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if !isRepoRoot(dir) {
		run(nil, "init", "-q", "-b", "main")
		run(nil, "config", "user.email", "t@example.com")
		run(nil, "config", "user.name", "t")
		run(nil, "config", "commit.gpgsign", "false")
	}
	run(nil, "add", "-A")
	run([]string{"GIT_AUTHOR_DATE=" + when, "GIT_COMMITTER_DATE=" + when}, "commit", "-q", "-m", subject)
}

func (f *fixture) proj(name string) string { return filepath.Join(f.home, "proj", name) }

// libs makes fake oden and ochazuke repos (ochazuke replaces oden with the
// checkout beside it), committed at 19:00.
func (f *fixture) libs() {
	f.t.Helper()
	f.write(filepath.Join(f.proj("oden"), "go.mod"), "module github.com/unixsuperhero/oden\n\ngo 1.26.0\n")
	commitAll(f.t, f.proj("oden"), "feat: oden", "2026-10-24T19:00:00Z")
	f.write(filepath.Join(f.proj("ochazuke"), "go.mod"), "module github.com/unixsuperhero/ochazuke\n\ngo 1.26.0\n\nrequire (\n\tgithub.com/unixsuperhero/oden v0.0.0-00010101000000-000000000000\n\tgolang.org/x/sys v0.47.0 // indirect\n)\n\nreplace github.com/unixsuperhero/oden => ../oden\n")
	commitAll(f.t, f.proj("ochazuke"), "feat: ochazuke", "2026-10-24T19:00:00Z")
}

// consumer builds o-cons, a real Go binary of module example.com/o-cons
// that answers catalog, and its repo ~/proj/o-cons whose go.mod replaces
// oden and requires ochazuke v0.3.0; the binary's mtime is built.
func (f *fixture) consumer(built time.Time) {
	f.t.Helper()
	src := f.t.TempDir()
	f.write(filepath.Join(src, "go.mod"), "module example.com/o-cons\n\ngo 1.26.0\n")
	f.write(filepath.Join(src, "main.go"), "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tif len(os.Args) > 1 && os.Args[1] == \"catalog\" {\n\t\tfmt.Println(`{\"schema\":1,\"name\":\"o-cons\",\"version\":\"v1\",\"nouns\":[{\"name\":\"thing\",\"verbs\":[{\"name\":\"list\",\"kind\":\"application\",\"rows\":\"thing\"}]}],\"bindings\":[],\"pages\":[]}`)\n\t\treturn\n\t}\n\tos.Exit(1)\n}\n")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", filepath.Join(f.bin, "o-cons"), ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("go build: %v\n%s", err, out)
	}
	if err := os.Chtimes(filepath.Join(f.bin, "o-cons"), built, built); err != nil {
		f.t.Fatal(err)
	}
	f.write(filepath.Join(f.proj("o-cons"), "go.mod"), "module example.com/o-cons\n\ngo 1.26.0\n\nrequire github.com/unixsuperhero/oden v0.0.0-00010101000000-000000000000\nrequire github.com/unixsuperhero/ochazuke v0.3.0\n\nreplace github.com/unixsuperhero/oden => "+f.proj("oden")+"\n")
}

// root installs a fake o answering catalog (oden's command noun) and
// command state: one row per listed o-NAME in ~/bin, plus x, whose
// ~/other/o-x it shadows.
func (f *fixture) root(listed ...string) {
	f.t.Helper()
	f.write(filepath.Join(f.data, "o.catalog"), `{"schema":1,"name":"o","version":"v0.0.0-x","state":"command.state","nouns":[{"name":"command","verbs":[{"name":"list","kind":"application","rows":"command"},{"name":"show","kind":"application","positional":["name"]},{"name":"state","kind":"application"}]}],"bindings":[],"pages":[]}`)
	var lines []string
	line := func(name, path string, shadows []map[string]string) {
		if shadows == nil {
			shadows = []map[string]string{}
		}
		b, _ := json.Marshal(map[string]any{"noun": "command", "id": name, "label": name + "  external  " + path,
			"data": map[string]any{"kind": "external", "path": path, "source": path, "aliases": []string{}, "shadows": shadows, "prefix": "o"}})
		lines = append(lines, string(b))
	}
	for _, name := range listed {
		line(name, filepath.Join(f.bin, "o-"+name), nil)
	}
	other := filepath.Join(f.home, "other", "o-x")
	line("x", filepath.Join(f.bin, "o-x"), []map[string]string{{"name": "x", "kind": "external", "source": other}})
	lines = append(lines, `{"noun":"command","id":"help","label":"help  inline  oden/builtins.go:22","data":{"kind":"inline","source":"oden/builtins.go:22","aliases":["-h","--help"],"shadows":[]}}`)
	f.write(filepath.Join(f.data, "o.state"), strings.Join(lines, "\n")+"\n")
	script := "#!/bin/sh\ncase \"$1 $2\" in\n  catalog*) /bin/cat '" + filepath.Join(f.data, "o.catalog") + "' ;;\n  'command state') /bin/cat '" + filepath.Join(f.data, "o.state") + "' ;;\n  *) exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(f.bin, "o"), []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// family is the fixture plus the libs, o-cons, o listing o-alpha, o-proj
// and o-cons, mtm (a root by the seeded repos.json roots), o-kit (a root by
// its catalog's command noun, unlisted) and o-plain (unlisted, no root).
func familyFixture(t *testing.T, built time.Time) *fixture {
	f := newFixture(t)
	f.libs()
	f.consumer(built)
	f.tool("mtm", projCatalog)
	f.tool("o-kit", `{"schema":1,"name":"kit","version":"v1","nouns":[{"name":"command","verbs":[{"name":"list","kind":"application","rows":"command"}]}],"bindings":[],"pages":[]}`)
	f.tool("o-plain", projCatalog)
	f.root("alpha", "cons", "proj")
	return f
}

func TestSnapshotRecordsRootLibsDepsAndReachability(t *testing.T) {
	f := familyFixture(t, time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC))
	f.mustRun("snapshot")
	m := byID(f.latest())
	if r := m["tool:o"]; r["catalog"] != true || r.Str("state") != "command.state" || r["reachable"] != nil {
		t.Fatalf("tool o %v", r)
	}
	if _, ok := m["noun:o/command"]; !ok {
		t.Fatal("o's command noun is missing")
	}
	for _, id := range []string{"tool:o", "tool:mtm", "tool:o-kit"} {
		if r := m[id]; r["root"] != true || r["reachable"] != nil {
			t.Fatalf("root %s %v", id, r)
		}
	}
	if r := m["tool:o-plain"]; r["reachable"] != false || r["root"] != nil {
		t.Fatalf("plain %v", r)
	}
	for _, id := range []string{"tool:o-alpha", "tool:o-proj", "tool:o-cons"} {
		if _, ok := m[id]["reachable"]; ok {
			t.Fatalf("%s %v", id, m[id])
		}
		if _, ok := m[id]["root"]; ok {
			t.Fatalf("%s %v", id, m[id])
		}
	}
	if r := m["tool:o-cons"]; r.Str("repo") != "o-cons" || r.Str("module") != "example.com/o-cons" {
		t.Fatalf("o-cons %v", r)
	}
	if r := m["shadow:o/x/~/other/o-x"]; r.Str("winner") != "~/bin/o-x" || r.Str("claimKind") != "external" {
		t.Fatalf("shadow %v (ids %v)", r, ids(f.latest()))
	}
	oden := m["lib:oden"]
	if oden.Str("path") != "~/proj/oden" || len(oden.Str("commit")) != 7 || oden["dirty"] != false || oden.Str("version") != oden.Str("commit") || oden.Str("goModule") != "github.com/unixsuperhero/oden" {
		t.Fatalf("lib oden %v", oden)
	}
	if r := m["lib:ochazuke"]; r.Str("goModule") != "github.com/unixsuperhero/ochazuke" {
		t.Fatalf("lib ochazuke %v", r)
	}
	for id, want := range map[string]string{
		"dep:o-cons/oden":     "tool replace ~/proj/oden v0.0.0-00010101000000-000000000000",
		"dep:o-cons/ochazuke": "tool require  v0.3.0",
		"dep:ochazuke/oden":   "lib replace ~/proj/oden v0.0.0-00010101000000-000000000000",
	} {
		r := m[id]
		if got := strings.Join([]string{r.Str("consumer"), r.Str("mode"), r.Str("replace"), r.Str("version")}, " "); got != want {
			t.Fatalf("%s: %q, want %q (%v)", id, got, want, r)
		}
	}
	if _, ok := m["dep:o-alpha/oden"]; ok {
		t.Fatal("a tool with no repo has deps")
	}
	if out := f.mustRun("diff"); !strings.Contains(out, "! shadow:o/x/~/other/o-x\n") || !strings.Contains(out, "+ lib:oden\n") {
		t.Fatalf("diff:\n%s", out)
	}

	// ochazuke moves on: the changelog's first section says so.
	old := m["lib:ochazuke"].Str("commit")
	f.write(filepath.Join(f.proj("ochazuke"), "x.go"), "package ochazuke\n")
	commitAll(t, f.proj("ochazuke"), "feat: more", "2026-10-24T21:00:00Z")
	got := newestEntry(t, f)
	now := byID(f.latest())["lib:ochazuke"].Str("commit")
	want := "## 2026-10-24 21:10\n\n### Root and libraries\n- changed: `ochazuke` commit `" + now + "`; was `" + old + "`\n"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("entry:\n%s\nwant prefix:\n%s", got, want)
	}
	entries, _ := f.tool0().Entries("")
	first := entries[len(entries)-1].Text
	for _, line := range []string{
		"### Root and libraries\n",
		"- added: `o-cons` → `oden` by replace `~/proj/oden`\n",
		"- added: `o-cons` → `ochazuke` by require `v0.3.0`\n",
		"- added: library `oden` at `~/proj/oden` (",
		"- shadow: `o x` runs `~/bin/o-x`; external `~/other/o-x` is shadowed\n",
	} {
		if !strings.Contains(first, line) {
			t.Fatalf("first entry lacks %q:\n%s", line, first)
		}
	}
	if strings.Index(first, "### Root and libraries") > strings.Index(first, "### Tools") {
		t.Fatalf("Root and libraries is not before Tools:\n%s", first)
	}
}

func TestCheckStaleBuildAndUnreachable(t *testing.T) {
	f := familyFixture(t, time.Date(2026, 10, 24, 18, 0, 0, 0, time.UTC)) // before the libs' 19:00 commits
	out, _ := f.run("check", "--jsonl")
	for _, want := range []string{
		`{"kind":"stale-build","subject":"tool:o-cons","detail":"~/bin/o-cons built 2026-10-24T18:00:00Z, before oden `,
		`{"kind":"unreachable","subject":"tool:o-plain","detail":"o does not list o-plain (only o-* executables are o's commands)"}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("check lacks %q:\n%s", want, out)
		}
	}
	for _, root := range []string{"tool:o", "tool:mtm", "tool:o-kit"} {
		if strings.Contains(out, `"subject":"`+root+`"`) {
			t.Fatalf("root %s is a problem:\n%s", root, out)
		}
	}
	if strings.Contains(out, "before ochazuke") {
		t.Fatalf("a require dep made a stale build:\n%s", out)
	}
	if strings.Contains(out, `"subject":"tool:o-alpha","detail":"o does not list`) {
		t.Fatalf("o-alpha is listed by o:\n%s", out)
	}
}

func TestOverviewTreeOfTheLatestSnapshot(t *testing.T) {
	f := familyFixture(t, time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC))
	if _, err := f.run("overview"); err == nil {
		t.Fatal("overview without a snapshot succeeded")
	}
	f.mustRun("snapshot")
	text := f.mustRun("overview")
	if alt := f.mustRun("inventory", "overview"); !strings.Contains(alt, `"libs (2)"`) {
		t.Fatalf("inventory overview:\n%s", alt)
	}
	oden := byID(f.latest())["lib:oden"].Str("commit")
	for _, want := range []string{
		"libs (2)\n",
		"  oden      " + oden + "  dirty: no  deps: o-cons, ochazuke\n",
		"root (3)\n  o      ~/bin/o      → alpha, cons, proj\n  mtm    ~/bin/mtm    1 noun 1 verb\n  o-kit  ~/bin/o-kit  1 noun 1 verb\n",
		"tools (4)\n",
		"  o-plain  ",
		"not reachable through o",
		"  o-alpha  ",
		"1 noun 2 verbs  host: nabe (alpha)\n",
		"hosts (1)\n  nabe: alpha (order 0)\n",
		"projects (2)\n  alpha  ~/proj/alpha  hub/o: yes  github: no  origin: o-proj\n  gone   ~/proj/gone   hub/o: no   github: no  (no origin)\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("overview lacks %q:\n%s", want, text)
		}
	}
	var ov Overview
	if err := json.Unmarshal([]byte(f.mustRun("overview", "--json")), &ov); err != nil {
		t.Fatal(err)
	}
	if len(ov.Libs) != 2 || len(ov.Root) != 3 || ov.Root[0].Bin != "~/bin/o" || ov.Root[1].Name != "mtm" || ov.Root[2].Name != "o-kit" || len(ov.Tools) != 4 || len(ov.Projects) != 2 || ov.Projects[0].Name != "alpha" {
		t.Fatalf("tree %+v", ov)
	}
}
