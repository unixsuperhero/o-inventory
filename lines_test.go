package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// entry is the newest changelog entry after a second snapshot.
func newestEntry(t *testing.T, f *fixture) string {
	t.Helper()
	f.clock = f.clock.Add(3600e9)
	f.mustRun("snapshot")
	entries, err := f.tool0().Entries("")
	if err != nil || len(entries) < 2 {
		t.Fatalf("entries %v %v", entries, err)
	}
	return entries[0].Text
}

func TestChangelogLinesForRemovalsAndChangedFields(t *testing.T) {
	f := newFixture(t)
	f.mustRun("snapshot")
	os.Remove(filepath.Join(f.bin, "o-alpha"))
	f.tool("o-proj", strings.Replace(projCatalog, `"version":"v1"`, `"version":"v2"`, 1))
	f.register("alpha", filepath.Join(f.bin, "o-alpha"), alphaCatalog(""))
	got := newestEntry(t, f)
	want := "## 2026-10-24 21:10\n\n" +
		"### Tools\n" +
		"- removed: `o-alpha` from `~/bin/o-alpha`, 1 noun, 2 verbs, 1 param, 1 binding, 1 page\n" +
		"- changed: `o-proj` version `v2`; was `v1`\n"
	if got != want {
		t.Fatalf("entry:\n%s\nwant:\n%s", got, want)
	}
}

func TestChangelogBindingRequestAndHostOrder(t *testing.T) {
	f := newFixture(t)
	f.mustRun("snapshot")
	f.tool("o-alpha", strings.Replace(alphaCatalog(""), `"keys":"x","request":{"noun":"skill","verb":"show"}`, `"keys":"x","request":{"noun":"skill","verb":"list"}`, 1))
	reg := filepath.Join(f.home, ".local", "share", "nabe", "catalogs", "alpha.json")
	data, _ := os.ReadFile(reg)
	os.WriteFile(reg, []byte(strings.Replace(string(data), `"bin":`, `"order":2,"bin":`, 1)), 0o644)
	got := newestEntry(t, f)
	for _, line := range []string{
		"### Keybindings\n- changed: `x` in o-alpha normal, `skill.list`; was `skill.show`\n",
		"### Hosts\n- changed: nabe `alpha` order 2; was 0\n",
	} {
		if !strings.Contains(got, line) {
			t.Fatalf("entry lacks %q:\n%s", line, got)
		}
	}
}

func TestParseFlagsInAnyOrder(t *testing.T) {
	f, err := parseFlags("x", []string{"a", "--commit", "--repo", "r", "b", "--quiet"}, []string{"commit", "quiet"}, []string{"repo"})
	if err != nil || strings.Join(f.words, ",") != "a,b" || !f.bools["commit"] || !f.bools["quiet"] || f.values["repo"] != "r" {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := parseFlags("x", []string{"--nope"}, nil, nil); err == nil {
		t.Fatal("unknown option accepted")
	}
}
