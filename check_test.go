package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unixsuperhero/oden"
)

func TestCheckReportsDriftAndFails(t *testing.T) {
	f := newFixture(t)
	gitRepo(t, filepath.Join(f.home, "proj", "alpha"), map[string]string{"feat: one": "2026-10-24T19:00:00Z"})
	f.tool("o-story", "not json")
	f.register("alpha", filepath.Join(f.bin, "o-alpha"), strings.Replace(alphaCatalog(""), `"0.1.0"`, `"0.0.9"`, 1))
	f.register("old", filepath.Join(f.bin, "o-old"), projCatalog)
	f.mustRun("repos") // seed: alpha, and the three framework repos (missing here)
	out, err := f.run("check", "--jsonl")
	var exit *oden.ExitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("check err %v", err)
	}
	want := []string{
		`{"kind":"missing-bin","subject":"host:nabe/old","detail":"~/bin/o-old is missing"}`,
		`{"kind":"missing-repo","subject":"repo:matomeru","detail":"~/proj/matomeru is not a repository"}`,
		`{"kind":"missing-repo","subject":"repo:ochazuke","detail":"~/proj/ochazuke is not a repository"}`,
		`{"kind":"missing-repo","subject":"repo:oden","detail":"~/proj/oden is not a repository"}`,
		`{"kind":"missing-tree","subject":"project:gone","detail":"~/proj/gone is missing"}`,
		`{"kind":"no-catalog","subject":"tool:o-story","detail":"~/bin/o-story catalog fails"}`,
		`{"kind":"no-hook","subject":"repo:alpha","detail":"~/proj/alpha has no o-inventory post-commit hook (o inv hook install alpha)"}`,
		`{"kind":"stale-catalog","subject":"host:nabe/alpha","detail":"registered catalog 0.0.9; o-alpha answers 0.1.0"}`,
	}
	if got := strings.Split(strings.TrimSpace(out), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("check:\n%s\nwant:\n%s", out, strings.Join(want, "\n"))
	}
}
