package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/unixsuperhero/ochazuke"
)

// tableFixture is a world with a row of every list noun: two snapshots,
// so there is a change row too.
func tableFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.libs()
	f.storyTool(storyState, map[string]string{"chrome/wide": `{"skip":"gap: the footer cuts at 80"}`})
	f.mtmTool(mtmCatalog(true), storeTables("1"))
	f.mustRun("snapshot")
	f.tool("o-alpha", alphaCatalog("grep"))
	f.clock = f.clock.Add(60e9)
	f.mustRun("snapshot")
	return f
}

// Every list noun declares columns in the catalog, the ones table.go
// gives it, in order.
func TestCatalogDeclaresEveryListNounsColumns(t *testing.T) {
	f := newFixture(t)
	var doc struct {
		Nouns []struct {
			Name    string            `json:"name"`
			Columns []ochazuke.Column `json:"columns"`
		} `json:"nouns"`
	}
	if err := json.Unmarshal([]byte(f.mustRun("catalog")), &doc); err != nil {
		t.Fatal(err)
	}
	declared := map[string][]ochazuke.Column{}
	for _, n := range doc.Nouns {
		declared[n.Name] = n.Columns
	}
	for _, noun := range rowNouns {
		tb, ok := tables[noun]
		if !ok {
			t.Errorf("%s has no table", noun)
			continue
		}
		if !slices.Equal(declared[noun], tb.columns) {
			t.Errorf("%s columns %v, want %v", noun, declared[noun], tb.columns)
		}
	}
	for _, noun := range []string{"inventory", "daily"} {
		if declared[noun] != nil {
			t.Errorf("%s declares columns %v", noun, declared[noun])
		}
	}
}

// Every row carries one cell per column of its noun, in a list and in the
// state, and the cells read the row's fields.
func TestEveryRowCarriesItsCells(t *testing.T) {
	f := tableFixture(t)
	first := map[string][]string{}
	for _, noun := range rowNouns {
		var rows []ListRow
		if err := json.Unmarshal([]byte(f.mustRun(noun, "list")), &rows); err != nil {
			t.Fatal(noun, err)
		}
		if len(rows) == 0 {
			t.Errorf("no %s rows", noun)
		}
		for _, r := range rows {
			if len(r.Cells) != len(tables[noun].columns) || r.Cells[0] == "" {
				t.Errorf("%s cells %q for %d columns", r.ID, r.Cells, len(tables[noun].columns))
			}
		}
		if len(rows) > 0 {
			first[noun] = rows[0].Cells
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(f.mustRun("state")), "\n") {
		var r struct {
			Noun  string   `json:"noun"`
			ID    string   `json:"id"`
			Cells []string `json:"cells"`
		}
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if len(r.Cells) != len(tables[r.Noun].columns) {
			t.Errorf("state %s cells %q", r.ID, r.Cells)
		}
	}
	for noun, want := range map[string][]string{
		"tool":    {"mtm", "v1", "", "", "yes", "root"},
		"page":    {"o-alpha", "main", "Main"},
		"project": {"alpha", "o-proj", "open", "yes", "~/proj/alpha"},
		"story":   {"chrome/wide", "chrome", "wide footer", "gap: the footer cuts at 80"},
		"table":   {"mtm", "history", "history", "seq", "1", "yes"},
		"change":  {"added", "verb", "verb:o-alpha/skill.grep", "", "2026-10-24T20:11:00Z"},
	} {
		if !slices.Equal(first[noun], want) {
			t.Errorf("%s first cells %q, want %q", noun, first[noun], want)
		}
	}
}
