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

// TestRowNounsAreOInventorysOwn: page and binding are o-inventory's row
// kinds (DefineOwn), not the framework's app.page and app.binding
// (ochazuke v0.16.0), which keep their own verbs under app.
func TestRowNounsAreOInventorysOwn(t *testing.T) {
	r := newFixture(t).tool0().shell().Runtime
	for _, noun := range []string{"page", "binding"} {
		if got := r.CanonicalNoun(noun); got != noun {
			t.Errorf("%s resolves to %s", noun, got)
		}
		if op, ok := r.Operation(noun + ".list"); !ok || op.Builtin {
			t.Errorf("%s.list is not o-inventory's: %+v", noun, op)
		}
		if op, ok := r.Operations["app."+noun+".list"]; ok {
			t.Errorf("app.%s.list exists: %+v", noun, op)
		}
	}
	if _, ok := r.Operation("app.page.next"); !ok {
		t.Error("the framework's app.page.next is gone")
	}
}

// TestCommandNounMatchesEitherSpelling: o's command rows are command in a
// plain oden catalog and app.command in an ochazuke v0.16.0 one.
func TestCommandNounMatchesEitherSpelling(t *testing.T) {
	plain := &ocli.CatalogDoc{Nouns: []ocli.CatalogNoun{{Name: "command"}}}
	namespaced := &ocli.CatalogDoc{Nouns: []ocli.CatalogNoun{{Name: ocli.CommandNoun}}, Aliases: map[string]string{"command": ocli.CommandNoun}}
	for _, doc := range []*ocli.CatalogDoc{plain, namespaced} {
		if !hasNoun(doc, "command") || !isCommandNoun(doc, "command") || !isCommandNoun(doc, ocli.CommandNoun) {
			t.Errorf("%+v: command not matched", doc)
		}
		if isCommandNoun(doc, "tool") {
			t.Errorf("%+v: tool matched", doc)
		}
	}
}
