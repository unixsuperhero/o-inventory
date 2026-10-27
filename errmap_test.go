package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func scanFixture(t *testing.T) *emReport {
	t.Helper()
	rep, err := ScanErrors(filepath.Join("testdata", "errmap"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// siteOf is the one site whose message is msg.
func siteOf(t *testing.T, rep *emReport, msg string) *emSite {
	t.Helper()
	var found *emSite
	for _, s := range rep.Sites {
		if s.Message == msg {
			if found != nil {
				t.Fatalf("two sites say %q", msg)
			}
			found = s
		}
	}
	if found == nil {
		t.Fatalf("no site says %q", msg)
	}
	return found
}

func TestErrorsSitesAndVerbs(t *testing.T) {
	rep := scanFixture(t)
	cases := []struct {
		msg, kind, class, fn string
		ops, via             []string
		cond                 string
	}{
		{"no pane is attached", "errors.New", "state", "(*App).define.func1", []string{"pane.close"}, nil, ""},
		{"unreachable: quit twice", "errors.New", "internal", "(*App).define.func2", []string{"app.quit"}, nil, ""},
		{"invariant: no runtime", "panic", "internal", "(*App).define", nil, nil, "a.r == nil"},
		{"missing name", "fmt.Errorf", "input", "(*App).renamePane", []string{"pane.rename"}, nil, `c.Name == ""`},
		{"could not save {c.Name}", "status", "io", "(*App).renamePane", []string{"pane.rename"}, nil, "err != nil"},
		{"write %s: %w", "fmt.Errorf", "io", "(*App).save", nil, []string{"pane.rename"}, "err != nil"},
		{"done: %w", "fmt.Errorf", "state", "(*App).doneTask", []string{"task.done"}, nil, "errors.Is(err, store.ErrGone)"},
		{"mode %q", "fmt.Errorf", "internal", "(*App).doneTask", []string{"task.done"}, nil, "switch c.Name default"},
		{"pane is gone", "sentinel", "state", "(package)", nil, nil, ""},
		{"no macro recording", "errors.New", "state", "(*App).defineMore.func2", []string{"macro.stop"}, nil, ""},
		{"nothing to undo", "errors.New", "state", "(*App).defineMore.func3", []string{"input.undo"}, nil, ""},
		{"no focused input", "errors.New", "state", "(*App).defineMore.func5", []string{"input.redo", "input.undo"}, nil, "a.r == nil"},
	}
	for _, c := range cases {
		s := siteOf(t, rep, c.msg)
		if s.Kind != c.kind || s.Class != c.class || s.Func != c.fn || s.Condition != c.cond {
			t.Errorf("%q: kind %s class %s func %s cond %q; want %s %s %s %q", c.msg, s.Kind, s.Class, s.Func, s.Condition, c.kind, c.class, c.fn, c.cond)
		}
		if !reflect.DeepEqual(s.Operations, c.ops) || !reflect.DeepEqual(s.ReachedFrom, c.via) {
			t.Errorf("%q: operations %v via %v; want %v via %v", c.msg, s.Operations, s.ReachedFrom, c.ops, c.via)
		}
	}
	if s := siteOf(t, rep, "write %s: %w"); !s.Wrapped || !reflect.DeepEqual(s.Wraps, []string{"err"}) {
		t.Errorf("write: wrapped %v wraps %v", s.Wrapped, s.Wraps)
	}
	if s := siteOf(t, rep, "done: %w"); !s.Failed || !reflect.DeepEqual(s.Sentinels, []string{"ErrGone"}) {
		t.Errorf("done: failed %v sentinels %v", s.Failed, s.Sentinels)
	}
	if s := siteOf(t, rep, "{err}"); s.Kind != "failed" || !s.Relay {
		t.Errorf("Failed(err): kind %s relay %v", s.Kind, s.Relay)
	}
	for _, s := range rep.Sites {
		if s.Message == "saved" {
			t.Errorf("a status that reads as no error is a site: %+v", s)
		}
	}
	if len(rep.Unresolved) != 0 {
		t.Errorf("unresolved handlers: %+v", rep.Unresolved)
	}
}

func TestErrorsSentinelUses(t *testing.T) {
	s := siteOf(t, scanFixture(t), "pane is gone")
	var got []string
	for _, u := range s.Uses {
		got = append(got, u.Role+" "+u.File+" "+strings.Join(u.Operations, ",")+" "+strings.Join(u.Via, ","))
	}
	want := []string{"compare app.go task.done ", "wrap app.go task.done ", "return store/store.go  task.done"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("uses %q, want %q", got, want)
	}
	test := siteOf(t, scanFixture(t), "test only")
	if !test.Test || test.Sentinel != "errTest" {
		t.Errorf("test sentinel %+v", test)
	}
}

func TestErrorsOutputIsDeterministic(t *testing.T) {
	render := func() (string, string) {
		rep := scanFixture(t)
		var md, jl bytes.Buffer
		rep.WriteMarkdown(&md, "o-inventory errors testdata/errmap")
		if err := rep.WriteJSONL(&jl); err != nil {
			t.Fatal(err)
		}
		return md.String(), jl.String()
	}
	md1, jl1 := render()
	for range 3 {
		if md, jl := render(); md != md1 || jl != jl1 {
			t.Fatal("two scans of one tree differ")
		}
	}
	for _, want := range []string{"## Summary", "### pane.rename", "### via pane.rename", "## Relays", "## Sentinels", "`ErrGone`", "## Test-only sentinels"} {
		if !strings.Contains(md1, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}
}

func TestErrorsCommandWritesFiles(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	var out bytes.Buffer
	tool := &Tool{Stdout: &out, Stderr: &out, Home: dir, Cwd: cwd}
	md, jl := filepath.Join(dir, "e.md"), filepath.Join(dir, "e.jsonl")
	if err := tool.ErrorsCommand([]string{"--md", md, "--jsonl", jl, "testdata/errmap"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(jl)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n < 10 {
		t.Errorf("%d JSON lines", n)
	}
	if b, _ := os.ReadFile(md); !strings.Contains(string(b), "o-inventory errors --md ~/e.md --jsonl ~/e.jsonl") {
		t.Errorf("markdown names no regenerate command:\n%s", b)
	}
	if err := tool.ErrorsCommand(nil); err == nil {
		t.Error("no root is no error")
	}
}
