package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// Tables: every row noun declares its columns on the runtime noun (the
// catalog's columns), and every row of it, in a list, a show and the state,
// carries its cells in that order (ListRow.Cells, ochazuke.ListItem.Cells,
// cli.Row.Cells), so a host such as nabe draws the list as a table. A cell
// is a field the row already holds, never a fact read anew.

// table is a noun's columns and the cells of one of its rows.
type table struct {
	columns []ochazuke.Column
	cells   func(Row) []string
}

var right = ochazuke.AlignRight

var tables = map[string]table{
	"lib": {
		columns: []ochazuke.Column{{Title: "lib", Min: 8}, {Title: "version", Priority: 1}, {Title: "commit", Width: 7, Priority: 2}, {Title: "dirty", Priority: 3}, {Title: "path", Priority: 4}},
		cells: func(r Row) []string {
			return []string{r.Str("name"), r.Str("version"), r.Str("commit"), yes(r["dirty"], "dirty"), r.Str("path")}
		},
	},
	"dep": {
		columns: []ochazuke.Column{{Title: "tool", Min: 8}, {Title: "lib", Min: 8}, {Title: "mode", Priority: 2}, {Title: "version", Priority: 1}},
		cells: func(r Row) []string {
			version := r.Str("version")
			if v := r.Str("replace"); v != "" {
				version = v
			}
			return []string{r.Str("tool"), r.Str("lib"), r.Str("mode"), version}
		},
	},
	"tool": {
		columns: []ochazuke.Column{{Title: "tool", Min: 8}, {Title: "version", Priority: 2}, {Title: "repo", Priority: 3}, {Title: "commit", Width: 7, Priority: 4}, {Title: "catalog", Priority: 1}, {Title: "notes", Priority: 5}},
		cells: func(r Row) []string {
			catalog := "none"
			switch {
			case r.Str("alias") != "":
				catalog = "alias of " + r.Str("alias")
			case r["catalog"] == true:
				catalog = "yes"
			}
			var notes []string
			if r["root"] == true {
				notes = append(notes, "root")
			}
			if r["reachable"] == false {
				notes = append(notes, "not reachable through o")
			}
			return []string{r.Str("name"), r.Str("version"), r.Str("repo"), r.Str("commit"), catalog, strings.Join(notes, ", ")}
		},
	},
	"noun": {
		columns: []ochazuke.Column{{Title: "tool", Min: 8}, {Title: "noun", Min: 8}, {Title: "pin", Priority: 1}, {Title: "framework", Priority: 2}},
		cells: func(r Row) []string {
			return []string{r.Str("tool"), r.Str("noun"), r.Str("pin"), yes(r["framework"], "framework")}
		},
	},
	"verb": {
		columns: []ochazuke.Column{{Title: "tool", Min: 8}, {Title: "noun", Min: 8}, {Title: "verb", Min: 6}, {Title: "kind", Priority: 2}, {Title: "rows", Priority: 3}, {Title: "description", Priority: 1}},
		cells: func(r Row) []string {
			return []string{r.Str("tool"), r.Str("noun"), r.Str("verb"), r.Str("verbKind"), r.Str("rows"), oneLine(r.Str("description"))}
		},
	},
	"binding": {
		columns: []ochazuke.Column{{Title: "keys", Min: 6}, {Title: "tool", Min: 8}, {Title: "mode", Priority: 2}, {Title: "request", Priority: 1}, {Title: "when", Priority: 3}},
		cells: func(r Row) []string {
			keys := r.Str("keys")
			if keys == "" {
				keys = "<" + r.Str("pattern") + ">"
			}
			request := r.Str("request")
			if r["unbind"] == true {
				request = "(unbound)"
			}
			return []string{keys, r.Str("tool"), modeName(r.Str("mode")), request, r.Str("when")}
		},
	},
	"page": {
		columns: []ochazuke.Column{{Title: "tool", Min: 8}, {Title: "page", Min: 8}, {Title: "title", Priority: 1}},
		cells:   func(r Row) []string { return []string{r.Str("tool"), r.Str("page"), r.Str("title")} },
	},
	"host": {
		columns: []ochazuke.Column{{Title: "host", Min: 8}, {Title: "order", Align: right, Priority: 2}, {Title: "catalog", Priority: 3}, {Title: "bin", Priority: 1}},
		cells: func(r Row) []string {
			catalog := strings.TrimSpace(r.Str("catalogName") + " " + r.Str("catalogVersion"))
			return []string{r.Str("name"), number(r["order"]), catalog, r.Str("bin")}
		},
	},
	"project": {
		columns: []ochazuke.Column{{Title: "project", Min: 10}, {Title: "origin", Priority: 2}, {Title: "status", Priority: 3}, {Title: "linked", Priority: 4}, {Title: "path", Priority: 1}},
		cells: func(r Row) []string {
			return []string{r.Str("name"), r.Str("origin"), r.Str("status"), yes(r["hubLink"], "yes"), r.Str("path")}
		},
	},
	"story": {
		columns: []ochazuke.Column{{Title: "story", Min: 12}, {Title: "area", Priority: 2}, {Title: "name", Priority: 1}, {Title: "skip", Priority: 3}},
		cells: func(r Row) []string {
			name := r.Str("name")
			if name == "" {
				name = r.Str("label")
			}
			return []string{strings.TrimPrefix(r.ID(), "story:"), r.Str("area"), name, r.Str("skip")}
		},
	},
	"edge": {
		columns: []ochazuke.Column{{Title: "from", Min: 8}, {Title: "to", Min: 8}, {Title: "via", Priority: 1}},
		cells:   func(r Row) []string { return []string{r.Str("from"), r.Str("to"), r.Str("via")} },
	},
	"table": {
		columns: []ochazuke.Column{{Title: "tool", Min: 6}, {Title: "table", Min: 10}, {Title: "owner", Priority: 1}, {Title: "key", Priority: 2}, {Title: "version", Align: right, Priority: 3}, {Title: "append-only", Priority: 4}},
		cells: func(r Row) []string {
			return []string{r.Str("tool"), r.Str("name"), r.Str("owner"), r.Str("key"), number(r["version"]), yes(r["appendOnly"], "yes")}
		},
	},
	"change": {
		columns: []ochazuke.Column{{Title: "change", Min: 7}, {Title: "kind", Priority: 2}, {Title: "row", Min: 12}, {Title: "fields", Priority: 1}, {Title: "time", Priority: 3}},
		cells: func(r Row) []string {
			var fields []string
			switch fs := r["fields"].(type) {
			case []FieldChange:
				for _, f := range fs {
					fields = append(fields, f.Field)
				}
			case []any:
				for _, f := range fs {
					if m, ok := f.(map[string]any); ok {
						s, _ := m["field"].(string)
						fields = append(fields, s)
					}
				}
			}
			return []string{r.Str("verb"), r.Str("rowKind"), r.Str("row"), strings.Join(fields, ","), stampText(r.Str("stamp"))}
		},
	},
	"run": {
		columns: []ochazuke.Column{{Title: "time", Min: 20}, {Title: "rows", Align: right}, {Title: "changes", Align: right}, {Title: "added", Align: right, Priority: 1}, {Title: "changed", Align: right, Priority: 1}, {Title: "removed", Align: right, Priority: 1}},
		cells: func(r Row) []string {
			return []string{r.Str("time"), number(r["rows"]), number(r["changes"]), number(r["added"]), number(r["changed"]), number(r["removed"])}
		},
	},
}

// cellsOf is a row's cells when its noun has columns, else nil.
func cellsOf(noun string, r Row) []string {
	if t, ok := tables[noun]; ok {
		return t.cells(r)
	}
	return nil
}

// yes is word when v is true, else "".
func yes(v any, word string) string {
	if v == true {
		return word
	}
	return ""
}

// number is an integer field (an int as collected, a float64 as read back
// from a snapshot) as text; "" when absent.
func number(v any) string {
	switch n := v.(type) {
	case int:
		return strconv.Itoa(n)
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	}
	return ""
}

// stampText is a snapshot stamp as RFC 3339 UTC; "" for none.
func stampText(stamp string) string {
	if t := stampTime(stamp); !t.IsZero() {
		return t.UTC().Format(time.RFC3339)
	}
	return ""
}

// oneLine is s with its whitespace runs, newlines included, as one space.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
