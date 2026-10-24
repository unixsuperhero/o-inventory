package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSnapshotRowsFromFakeToolsRegistryAndStore(t *testing.T) {
	f := newFixture(t)
	f.tool("o-story", "not json") // on PATH, no catalog
	out := f.mustRun("snapshot")
	if !strings.Contains(out, "wrote ~/hub/inventory/20261024T201000Z.jsonl") {
		t.Fatalf("snapshot said %q", out)
	}
	want := []string{
		"binding:o-alpha/normal/skill/x",
		"binding:ochazuke/normal/app/q",
		"host:nabe/alpha",
		"noun:o-alpha/skill",
		"noun:o-proj/project",
		"noun:ochazuke/app",
		"page:o-alpha/main",
		"param:o-alpha/skill.list/format",
		"project:alpha",
		"project:gone",
		"tool:o-alpha",
		"tool:o-proj",
		"tool:o-story",
		"verb:o-alpha/skill.list",
		"verb:o-alpha/skill.show",
		"verb:o-proj/project.list",
		"verb:ochazuke/app.quit",
	}
	rows := f.latest()
	if got := ids(rows); !reflect.DeepEqual(got, want) {
		t.Fatalf("ids:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	m := byID(rows)
	if r := m["tool:o-story"]; r["catalog"] != nil {
		t.Fatalf("o-story without a catalog: %v", r)
	} else if _, ok := r["catalog"]; !ok {
		t.Fatalf("o-story row lacks catalog: null: %v", r)
	}
	if r := m["tool:o-alpha"]; r["catalog"] != true || r.Str("bin") != "~/bin/o-alpha" || r.Str("version") != "0.1.0" || r.Str("state") != "skill.state" {
		t.Fatalf("o-alpha row %v", r)
	}
	if r := m["project:alpha"]; r["hubLink"] != true || r.Str("origin") != "o-proj" || r.Str("path") != "~/proj/alpha" {
		t.Fatalf("project alpha %v", r)
	}
	if r := m["binding:ochazuke/normal/app/q"]; r.Str("request") != "app.quit" || r.Str("tool") != Framework {
		t.Fatalf("framework binding %v", r)
	}
	if r := m["host:nabe/alpha"]; r.Str("bin") != "~/bin/o-alpha" || r.Str("catalogVersion") != "0.1.0" {
		t.Fatalf("host %v", r)
	}

	// The same sources again: no file written.
	f.clock = f.clock.Add(60e9)
	if out := f.mustRun("snapshot"); !strings.HasPrefix(out, "no change") {
		t.Fatalf("second snapshot said %q", out)
	}
	files, _ := filepath.Glob(filepath.Join(f.home, "hub", "inventory", "*.jsonl"))
	if len(files) != 2 { // the stamp and latest
		t.Fatalf("files %v", files)
	}
	// A rebuilt binary (mtime) and a re-registration change nothing.
	os.Chtimes(filepath.Join(f.bin, "o-alpha"), f.clock, f.clock)
	f.register("alpha", filepath.Join(f.bin, "o-alpha"), alphaCatalog(""))
	if out := f.mustRun("snapshot", "--quiet"); out != "" {
		t.Fatalf("quiet no-change printed %q", out)
	}
}

func TestSnapshotSkipsSymlinkedDuplicateAndMountedNouns(t *testing.T) {
	f := newFixture(t)
	if err := os.Symlink(filepath.Join(f.bin, "o-alpha"), filepath.Join(f.bin, "o-zeta")); err != nil {
		t.Fatal(err)
	}
	// A host whose catalog carries alpha's mounted nouns as alpha-NOUN.
	f.tool("o-host", `{"schema":1,"name":"o-host","version":"v1","nouns":[
	  {"name":"alpha-skill","verbs":[{"name":"list","kind":"application","rows":"alpha-skill"}]},
	  {"name":"extension","verbs":[{"name":"list","kind":"application"}]}],
	  "bindings":[{"noun":"alpha-skill","mode":"normal","keys":"y","request":{"noun":"alpha-skill","verb":"list"}}],"pages":[]}`)
	f.mustRun("snapshot")
	m := byID(f.latest())
	if r := m["tool:o-zeta"]; r.Str("alias") != "o-alpha" {
		t.Fatalf("o-zeta %v", r)
	}
	if _, ok := m["noun:o-zeta/skill"]; ok {
		t.Fatal("the alias's nouns were recorded")
	}
	for id := range m {
		if strings.Contains(id, "alpha-skill") {
			t.Fatalf("mounted noun recorded: %s", id)
		}
	}
	if _, ok := m["noun:o-host/extension"]; !ok {
		t.Fatal("the host's own noun is missing")
	}
}
