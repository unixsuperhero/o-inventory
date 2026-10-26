package main

import (
	"encoding/json"
	"testing"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// Every verb o-inventory defines declares its history kind
// (Operation.HistoryKind) rather than leaving it to the default, and the
// catalog's history is that kind. The framework's own operations (a fresh
// runtime's) are left to ochazuke. A new verb without a kind fails here.
func TestEveryVerbDeclaresItsHistory(t *testing.T) {
	f := newFixture(t)
	var doc ocli.CatalogDoc
	if err := json.Unmarshal([]byte(f.mustRun("catalog")), &doc); err != nil {
		t.Fatal(err)
	}
	framework := ochazuke.NewRuntime().Operations
	ops := f.tool0().shell().Runtime.Operations
	n := 0
	for _, noun := range doc.Nouns {
		for _, v := range noun.Verbs {
			key := noun.Name + "." + v.Name
			if _, ok := framework[key]; ok {
				continue
			}
			n++
			if v.History == "" || ops[key].HistoryKind == "" || v.History != ops[key].HistoryKind {
				t.Errorf("catalog verb %s: history %q, declared %q", key, v.History, ops[key].HistoryKind)
			}
		}
	}
	if want := 2*len(rowNouns) + 2 + len(dailyVerbs); n != want {
		t.Fatalf("the catalog has %d o-inventory verbs, want %d", n, want)
	}

	for key, want := range map[string]string{
		"tool.list": ochazuke.HistoryFetch, "run.show": ochazuke.HistoryFetch,
		"inventory.state": ochazuke.HistoryFetch, "inventory.overview": ochazuke.HistoryFetch,
		"daily.status": ochazuke.HistoryFetch, "daily.plist": ochazuke.HistoryChange,
		"daily.install": ochazuke.HistoryChange, "daily.uninstall": ochazuke.HistoryChange, "daily.run": ochazuke.HistoryChange,
	} {
		if got := ops[key].HistoryKind; got != want {
			t.Errorf("%s: %q, want %q", key, got, want)
		}
	}
}
