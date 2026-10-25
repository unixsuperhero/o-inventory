package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNounHelpDoesNotExecute(t *testing.T) {
	for _, args := range [][]string{
		{"tool"},
		{"tool", "--help"},
		{"help", "tool"},
		{"tool", "show", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newFixture(t)
			// Invalid snapshot data makes accidental operation execution fail.
			path := filepath.Join(f.home, "hub", "inventory", Latest)
			f.write(path, "not JSON\n")
			out := f.mustRun(args...)
			for _, want := range []string{"tool", "show"} {
				if !strings.Contains(out, want) {
					t.Errorf("help missing %q:\n%s", want, out)
				}
			}
			if len(args) == 1 || args[1] != "show" {
				if !strings.Contains(out, "list") {
					t.Errorf("group help missing list:\n%s", out)
				}
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "not JSON\n" {
				t.Fatalf("help changed snapshot: %q, %v", data, err)
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil || len(entries) != 1 {
				t.Fatalf("help changed inventory files: %v, %v", entries, err)
			}
		})
	}

	f := newFixture(t)
	out, err := f.run("tool", "not-a-verb")
	if err == nil {
		t.Fatal("unknown verb succeeded")
	}
	if !strings.Contains(out+f.err.String()+err.Error(), "list") {
		t.Fatalf("unknown verb omitted available children: %s %s %v", out, f.err.String(), err)
	}
}

func TestCatalogAnswersAndBindingsValidate(t *testing.T) {
	f := newFixture(t)
	out := f.mustRun("catalog")
	var doc struct {
		Name  string `json:"name"`
		State string `json:"state"`
		Nouns []struct {
			Name  string `json:"name"`
			Verbs []struct {
				Name string `json:"name"`
				Rows string `json:"rows"`
			} `json:"verbs"`
		} `json:"nouns"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Name != "o-inventory" || doc.State != "inventory.state" {
		t.Fatalf("catalog %v: %s", err, out)
	}
	rows := map[string]string{}
	for _, n := range doc.Nouns {
		for _, v := range n.Verbs {
			if v.Name == "list" {
				rows[n.Name] = v.Rows
			}
		}
	}
	for _, noun := range rowNouns {
		if rows[noun] != noun {
			t.Errorf("%s list declares rows %q", noun, rows[noun])
		}
	}
	if _, err := f.tool0().shell().Runtime.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}

func TestNounsListShowAndStateFromTheLatestSnapshot(t *testing.T) {
	f := newFixture(t)
	f.libs()
	f.mustRun("snapshot")
	f.tool("o-alpha", alphaCatalog("grep"))
	f.clock = f.clock.Add(60e9)
	f.mustRun("snapshot")

	var tools []ListRow
	if err := json.Unmarshal([]byte(f.mustRun("tool", "list")), &tools); err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 || tools[0].ID != "tool:o-alpha" || tools[0].Label != "o-alpha 0.1.0" || tools[0].Data.Str("bin") != "~/bin/o-alpha" {
		t.Fatalf("tools %+v", tools)
	}
	var row Row
	if err := json.Unmarshal([]byte(f.mustRun("verb", "show", "--target", "verb:o-alpha/skill.grep")), &row); err != nil || row.ID() != "verb:o-alpha/skill.grep" {
		t.Fatalf("show --target: %v %v", err, row)
	}
	if err := json.Unmarshal([]byte(f.mustRun("binding", "show", "q in ochazuke normal → app.quit")), &row); err != nil || row.ID() != "binding:ochazuke/normal/app/q" {
		t.Fatalf("show by label: %v %v", err, row)
	}
	var changes []ListRow
	if err := json.Unmarshal([]byte(f.mustRun("change", "list")), &changes); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].ID != "change:20261024T201100Z/verb:o-alpha/skill.grep" || changes[0].Label != "added: `o-alpha skill grep`" {
		t.Fatalf("changes %+v", changes)
	}
	lines := strings.Split(strings.TrimSpace(f.mustRun("state")), "\n")
	seen := map[string]int{}
	for _, l := range lines {
		var r struct{ Noun, ID, Label string }
		if err := json.Unmarshal([]byte(l), &r); err != nil || r.ID == "" || r.Label == "" {
			t.Fatalf("state line %q: %v", l, err)
		}
		seen[r.Noun]++
	}
	for _, noun := range rowNouns {
		if seen[noun] == 0 {
			t.Errorf("state has no %s rows: %v", noun, seen)
		}
	}
	if f.mustRun("inventory", "state") != f.mustRun("state") {
		t.Fatal("state and inventory state differ")
	}
}
