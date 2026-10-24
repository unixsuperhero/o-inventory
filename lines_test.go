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

func TestHeadersStayPutWhenALaterSnapshotSharesTheMinute(t *testing.T) {
	f := newFixture(t)
	f.mustRun("snapshot")
	f.mustRun("changelog")
	f.tool("o-alpha", alphaCatalog("grep"))
	f.clock = f.clock.Add(30e9) // the same minute
	f.mustRun("snapshot")
	if out := f.mustRun("changelog"); out != "~/hub/CHANGELOG.md: 2 entries, 1 new\n" {
		t.Fatalf("changelog said %q", out)
	}
	data, _ := os.ReadFile(filepath.Join(f.home, "hub", "CHANGELOG.md"))
	if strings.Count(string(data), "\n## ") != 2 || !strings.Contains(string(data), "\n## 2026-10-24 20:10:30\n") || !strings.Contains(string(data), "\n## 2026-10-24 20:10\n") {
		t.Fatalf("headers:\n%s", data)
	}
}

func TestDuplicateKeyIDsDoNotDependOnCatalogOrder(t *testing.T) {
	a := `{"noun":"app","mode":"normal","keys":"q","request":{"noun":"app","verb":"quit"}}`
	b := `{"noun":"app","mode":"normal","keys":"q","request":{"noun":"app","verb":"stop"}}`
	cat := func(x, y string) string {
		return `{"schema":1,"name":"o-alpha","version":"1","nouns":[],"bindings":[` + x + `,` + y + `],"pages":[]}`
	}
	var got []string
	for _, c := range []string{cat(a, b), cat(b, a)} {
		f := newFixture(t)
		f.tool("o-alpha", c)
		f.mustRun("snapshot")
		got = append(got, byID(f.latest())["binding:ochazuke/normal/app/q#2"].Str("request"))
	}
	if got[0] != got[1] || got[0] == "" {
		t.Fatalf("q#2 is %v", got)
	}
}

func TestBindingWithoutAModeIsNamedEveryMode(t *testing.T) {
	v := (&Tool{}).newView(nil, nil, nil)
	r := Row{"kind": "binding", "id": "binding:ochazuke//app/ctrl+c", "tool": "ochazuke", "noun": "app", "keys": "ctrl+c", "request": "app.quit"}
	if got := v.addedText(r); got != "`ctrl+c` in ochazuke every mode, `app.quit`" {
		t.Fatalf("%q", got)
	}
}
