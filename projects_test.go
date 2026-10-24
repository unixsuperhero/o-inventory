package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Projects come only from the o-proj store: origin is the store row's
// field, hub/o is a link named after the project in $O_PROJ_HUB that
// resolves to the project's tree, and a link with no store row is no project.
func TestProjectsOnlyFromStoreWithResolvedHubLinks(t *testing.T) {
	f := newFixture(t)
	hub := filepath.Join(f.home, "elsewhere", "o") // $O_PROJ_HUB, not ~/hub/o
	proj := func(name string) string { return filepath.Join(f.home, "proj", name) }
	for _, d := range []string{hub, proj("right"), proj("wrong"), proj("other"), proj("missing"), proj("lib")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{
		"right": proj("right"),      // correct
		"wrong": proj("other"),      // wrong target
		"lib":   proj("lib"),        // a repo with a link but no store row
		"stale": proj("not-a-tree"), // dangling, no store row either
	} {
		if err := os.Symlink(target, filepath.Join(hub, name)); err != nil {
			t.Fatal(err)
		}
	}
	tree := func(p string) string { return `[{"kind":"clone","path":"` + p + `"}]` }
	f.write(filepath.Join(f.data, "o-proj.list"), `[`+
		`{"id":"p1","name":"right","status":"open","origin":"o-proj","default_tree":"`+proj("right")+`","trees":`+tree(proj("right"))+`},`+
		`{"id":"p2","name":"wrong","status":"open","origin":"o-proj","default_tree":"`+proj("wrong")+`","trees":`+tree(proj("wrong"))+`},`+
		`{"id":"p3","name":"missing","status":"open","trees":`+tree(proj("missing"))+`}]`)

	tl := f.tool0()
	tl.Env = append(tl.Env, "O_PROJ_HUB="+hub)
	f.out.Reset()
	if err := tl.Main(t.Context(), []string{"snapshot"}); err != nil {
		t.Fatalf("snapshot: %v\n%s", err, f.err.String())
	}
	m := map[string]Row{}
	for _, r := range f.latest() {
		if r.Str("kind") == "project" {
			m[r.Str("name")] = r
		}
	}
	if len(m) != 3 || m["lib"] != nil || m["stale"] != nil || m["alpha"] != nil {
		t.Fatalf("projects are the store's rows only: %v", m)
	}
	for name, want := range map[string]struct {
		hub    bool
		origin string
	}{"right": {true, "o-proj"}, "wrong": {false, "o-proj"}, "missing": {false, ""}} {
		r := m[name]
		if (r["hubLink"] == true) != want.hub || r.Str("origin") != want.origin || r.Str("path") != "~/proj/"+name {
			t.Fatalf("project %s: %v; want hubLink %v origin %q", name, r, want.hub, want.origin)
		}
	}

	f.out.Reset()
	if err := tl.Main(t.Context(), []string{"overview"}); err != nil {
		t.Fatal(err)
	}
	want := "projects (3)\n" +
		"  right    ~/proj/right    hub/o: yes  github: no  origin: o-proj\n" +
		"  wrong    ~/proj/wrong    hub/o: no   github: no  origin: o-proj\n" +
		"  missing  ~/proj/missing  hub/o: no   github: no  (no origin)\n"
	if text := f.out.String(); !strings.Contains(text, want) {
		t.Fatalf("overview lacks\n%s\nin:\n%s", want, text)
	}
}
