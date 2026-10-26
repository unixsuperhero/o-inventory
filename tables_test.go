package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mtmCatalog is a fake mtm's catalog, with the read-only store tables
// verb when tables is set.
func mtmCatalog(tables bool) string {
	store := ""
	if tables {
		store = `,{"name":"store","verbs":[{"name":"tables","kind":"application","rows":"store-table"}]},{"name":"store-table","verbs":[]}`
	}
	return `{"schema":1,"name":"mtm","version":"v1",
 "nouns":[{"name":"task","verbs":[{"name":"list","kind":"application","rows":"task"}]}` + store + `],
 "bindings":[],"pages":[]}`
}

// storeTables is mtm store tables' answer: a JSON array of store-table
// rows (the shell's form for a []cli.Row), row counts included.
func storeTables(projectsVersion string) string {
	return `[
  {"noun":"store-table","id":"history","label":"history","data":{"name":"history","owner":"history","key":"seq","version":1,"append_only":true,"rows":812},"cells":["history","history","seq","1","yes","812"]},
  {"noun":"store-table","id":"projects","label":"projects","data":{"name":"projects","owner":"work","key":"id","version":` + projectsVersion + `,"append_only":false,"rows":15},"cells":["projects","work","id","1","","15"]}
]`
}

// mtmTool installs a fake mtm answering catalog and store tables (from
// data/mtm.tables); any other words fail as an unknown command.
func (f *fixture) mtmTool(catalog, tables string) {
	f.t.Helper()
	f.write(filepath.Join(f.data, "mtm.catalog"), catalog)
	f.write(filepath.Join(f.data, "mtm.tables"), tables)
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"  catalog) /bin/cat '" + filepath.Join(f.data, "mtm.catalog") + "' ;;\n" +
		"  'store tables') /bin/cat '" + filepath.Join(f.data, "mtm.tables") + "' ;;\n" +
		"  *) echo \"mtm: command \\\"$1\\\" not found\" >&2; echo \"$*\" >> '" + filepath.Join(f.data, "mtm.unknown") + "'; exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(f.bin, "mtm"), []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func TestSnapshotStoreTablesFromMtm(t *testing.T) {
	f := newFixture(t)
	f.mtmTool(mtmCatalog(true), storeTables("1"))
	f.mustRun("snapshot")
	m := byID(f.latest())
	p := m["table:projects"]
	if p.Str("tool") != "mtm" || p.Str("name") != "projects" || p.Str("owner") != "work" || p.Str("key") != "id" || canon(p["version"]) != "1" || p["appendOnly"] != nil {
		t.Fatalf("projects %v", p)
	}
	if h := m["table:history"]; h["appendOnly"] != true || h.Str("owner") != "history" {
		t.Fatalf("history %v", h)
	}
	for _, r := range m {
		if r.Kind() == "table" && r["rows"] != nil {
			t.Fatalf("a row count was recorded: %v", r)
		}
	}

	// More rows written: no snapshot. A version bump: one changed line.
	f.mtmTool(mtmCatalog(true), strings.ReplaceAll(storeTables("1"), "812", "900"))
	f.clock = f.clock.Add(60e9)
	if out := f.mustRun("snapshot"); !strings.HasPrefix(out, "no change") {
		t.Fatalf("row counts made a snapshot: %q", out)
	}
	f.mtmTool(mtmCatalog(true), storeTables("2"))
	f.clock = f.clock.Add(3600e9)
	f.mustRun("snapshot")
	if out := f.mustRun("diff"); out != "~ table:projects  version: 1 -> 2\n" {
		t.Fatalf("diff:\n%s", out)
	}
	f.mustRun("changelog")
	data, err := os.ReadFile(filepath.Join(f.home, "hub", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "### Store\n- changed: table `projects` version 2; was 1\n") {
		t.Fatalf("changelog lacks the version line:\n%s", text)
	}
	if !strings.Contains(text, "- added: table `history` in mtm, owner `history`, key `seq`, version 1, append-only\n") {
		t.Fatalf("changelog lacks the added table:\n%s", text)
	}
}

// An mtm whose catalog has no store tables (built before the verb) is not
// asked; the snapshot says why it has no table rows.
func TestSnapshotSkipsStoreTablesOnAnOlderMtm(t *testing.T) {
	f := newFixture(t)
	f.mtmTool(mtmCatalog(false), storeTables("1"))
	f.mustRun("snapshot")
	for _, r := range f.latest() {
		if r.Kind() == "table" {
			t.Fatalf("table row from an mtm without the verb: %v", r)
		}
	}
	if !strings.Contains(f.err.String(), "warning: mtm has no store tables verb (built before matomeru 9c0e73b; reinstall mtm): table rows skipped") {
		t.Fatalf("stderr %q", f.err.String())
	}
	if _, err := os.Stat(filepath.Join(f.data, "mtm.unknown")); err == nil {
		t.Fatal("mtm was asked for a verb its catalog lacks")
	}
}
