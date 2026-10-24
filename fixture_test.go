package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fixture is a temporary world: a home with ~/bin holding fake tools that
// answer catalog from data files, a nabe registry, a hub with o/ links and
// an o-proj whose list prints a fake store.
type fixture struct {
	t     *testing.T
	home  string
	bin   string
	data  string
	clock time.Time
	out   bytes.Buffer
	err   bytes.Buffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home := t.TempDir()
	f := &fixture{t: t, home: home, bin: filepath.Join(home, "bin"), data: filepath.Join(home, "data"), clock: time.Date(2026, 10, 24, 20, 10, 0, 0, time.UTC)}
	for _, d := range []string{f.bin, f.data, filepath.Join(home, "hub", "o"), filepath.Join(home, "proj", "alpha")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.tool("o-alpha", alphaCatalog(""))
	f.tool("o-proj", projCatalog)
	f.write(filepath.Join(f.data, "o-proj.list"), `[{"id":"p1","name":"alpha","status":"open","origin":"o-proj","trees":[{"kind":"clone","path":"`+filepath.Join(home, "proj", "alpha")+`"}]},{"id":"p2","name":"gone","status":"open","trees":[{"kind":"dir","path":"`+filepath.Join(home, "proj", "gone")+`"}]}]`)
	if err := os.Symlink(filepath.Join(home, "proj", "alpha"), filepath.Join(home, "hub", "o", "alpha")); err != nil {
		t.Fatal(err)
	}
	f.register("alpha", filepath.Join(f.bin, "o-alpha"), alphaCatalog(""))
	return f
}

func (f *fixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// tool installs a fake BIN on PATH answering catalog (and list) from
// data/BIN.catalog (data/BIN.list).
func (f *fixture) tool(name, catalog string) {
	f.t.Helper()
	f.write(filepath.Join(f.data, name+".catalog"), catalog)
	script := "#!/bin/sh\ncase \"$1\" in\n  catalog) /bin/cat '" + filepath.Join(f.data, name+".catalog") + "' ;;\n  list) /bin/cat '" + filepath.Join(f.data, name+".list") + "' ;;\n  *) echo \"unknown command $1\" >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(f.bin, name), []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) register(name, bin, catalog string) {
	f.t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(catalog), &doc); err != nil {
		f.t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"bin": bin, "registeredAt": "2026-10-24T18:00:00Z", "catalog": doc})
	f.write(filepath.Join(f.home, ".local", "share", "nabe", "catalogs", name+".json"), string(b))
}

func (f *fixture) tool0() *Tool {
	return &Tool{
		Stdin: strings.NewReader(""), Stdout: &f.out, Stderr: &f.err, Home: f.home, Prog: "o-inventory",
		Env: []string{"PATH=" + f.bin, "HOME=" + f.home},
		Now: func() time.Time { return f.clock },
	}
}

// run runs one command line and returns its stdout.
func (f *fixture) run(args ...string) (string, error) {
	f.out.Reset()
	f.err.Reset()
	err := f.tool0().Main(context.Background(), args)
	return f.out.String(), err
}

func (f *fixture) mustRun(args ...string) string {
	f.t.Helper()
	out, err := f.run(args...)
	if err != nil {
		f.t.Fatalf("%v: %v\nstdout:\n%s\nstderr:\n%s", args, err, out, f.err.String())
	}
	return out
}

func (f *fixture) latest() []Row {
	f.t.Helper()
	rows, err := readRows(filepath.Join(f.home, "hub", "inventory", Latest))
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

// alphaCatalog is o-alpha's catalog; listParams are skill list's params.
func alphaCatalog(extraVerb string) string {
	verbs := `{"name":"list","kind":"application","rows":"skill","params":{"format":{"type":"string"}}},{"name":"show","kind":"application"}`
	if extraVerb != "" {
		verbs += `,{"name":"` + extraVerb + `","kind":"application"}`
	}
	return `{"schema":1,"name":"o-alpha","version":"0.1.0","state":"skill.state",
 "nouns":[{"name":"app","verbs":[{"name":"quit","kind":"interaction","result":"boolean"}]},
          {"name":"skill","verbs":[` + verbs + `]}],
 "bindings":[{"noun":"app","mode":"normal","keys":"q","request":{"noun":"app","verb":"quit"}},
             {"noun":"skill","mode":"normal","keys":"x","request":{"noun":"skill","verb":"show"}}],
 "pages":[{"id":"main","title":"Main"}]}`
}

const projCatalog = `{"schema":1,"name":"o-proj","version":"v1",
 "nouns":[{"name":"app","verbs":[{"name":"quit","kind":"interaction","result":"boolean"}]},
          {"name":"project","verbs":[{"name":"list","kind":"application","rows":"project"}]}],
 "bindings":[{"noun":"app","mode":"normal","keys":"q","request":{"noun":"app","verb":"quit"}}],
 "pages":[]}`

func ids(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.ID()
	}
	return out
}
