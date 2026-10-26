package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// compared are the fields a change is detected on, by row kind (the
// design's detection rules). Every other field is carried for reading and
// for diff --all: descriptions, registeredAt, binMtime, catalog versions a
// registry holds, row order.
var compared = map[string][]string{
	"lib":     {"path", "commit", "dirty", "goModule"},
	"dep":     {"mode", "version", "replace"},
	"shadow":  {"winner", "claimKind", "aliasOf"},
	"tool":    {"bin", "version", "commit", "catalog", "alias", "state", "reachable", "root"},
	"noun":    {"pin", "framework"},
	"verb":    {"verbKind", "result", "confirm", "repeatable", "rows", "quiet", "positional"},
	"param":   {"type", "required", "enum", "source", "free"},
	"binding": {"request", "args", "when", "unbind", "replay"},
	"page":    {"title"},
	"host":    {"order", "refreshEvery", "bin"},
	"project": {"origin", "hubLink", "status", "path", "registered"},
	"story":   {"skip"},
	"edge":    {},
	"table":   {"version", "owner", "key", "appendOnly"},
}

// neverCompared are ignored even by diff --all: they change on every
// registration, build or run without the fact changing.
var neverCompared = map[string]bool{"kind": true, "id": true, "registeredAt": true, "created": true, "binMtime": true}

// FieldChange is one compared field that differs.
type FieldChange struct {
	Field string `json:"field"`
	Was   any    `json:"was"`
	Now   any    `json:"now"`
}

// Change is a row added, removed, or changed in compared fields, by id.
type Change struct {
	Verb   string        `json:"verb"` // added (shadow for a shadow row), changed, removed
	ID     string        `json:"row"`
	Kind   string        `json:"rowKind"`
	Fields []FieldChange `json:"fields,omitempty"`
	Old    Row           `json:"-"`
	New    Row           `json:"-"`
}

// Row is the row the change is about: the new one, else the removed one.
func (c Change) Row() Row {
	if c.New != nil {
		return c.New
	}
	return c.Old
}

// Diff is the changes from a to b, sorted by row id. all compares every
// field but neverCompared instead of the kind's compared list.
func Diff(a, b []Row, all bool) []Change {
	old, cur := byID(a), byID(b)
	var out []Change
	for id, r := range cur {
		o, ok := old[id]
		if !ok {
			verb := "added"
			if r.Kind() == "shadow" {
				verb = "shadow" // a claim newly shadowed is its own change
			}
			out = append(out, Change{Verb: verb, ID: id, Kind: r.Kind(), New: r})
			continue
		}
		if fields := changedFields(o, r, all); len(fields) > 0 {
			out = append(out, Change{Verb: "changed", ID: id, Kind: r.Kind(), Fields: fields, Old: o, New: r})
		}
	}
	for id, o := range old {
		if _, ok := cur[id]; !ok {
			out = append(out, Change{Verb: "removed", ID: id, Kind: o.Kind(), Old: o})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func changedFields(o, r Row, all bool) []FieldChange {
	fields := compared[r.Kind()]
	if all {
		set := map[string]bool{}
		for k := range o {
			set[k] = true
		}
		for k := range r {
			set[k] = true
		}
		fields = nil
		for k := range set {
			if !neverCompared[k] {
				fields = append(fields, k)
			}
		}
		sort.Strings(fields)
	}
	var out []FieldChange
	for _, f := range fields {
		if canon(o[f]) != canon(r[f]) {
			out = append(out, FieldChange{Field: f, Was: o[f], Now: r[f]})
		}
	}
	return out
}

// changeRow is a change as a row of kind change: id change:STAMP/ROW_ID.
func changeRow(stamp string, c Change) Row {
	r := newRow("change", stamp+"/"+c.ID)
	r["verb"], r["row"], r["rowKind"], r["stamp"] = c.Verb, c.ID, c.Kind, stamp
	if len(c.Fields) > 0 {
		r["fields"] = c.Fields
	}
	return r
}

// readable is a change as one line: + ID, ! ID (shadow), - ID, or ~ ID
// field: was -> now.
func readable(c Change) string {
	switch c.Verb {
	case "added":
		return "+ " + c.ID
	case "shadow":
		return "! " + c.ID
	case "removed":
		return "- " + c.ID
	}
	parts := make([]string, len(c.Fields))
	for i, f := range c.Fields {
		parts[i] = f.Field + ": " + canon(f.Was) + " -> " + canon(f.Now)
	}
	return "~ " + c.ID + "  " + strings.Join(parts, "; ")
}

// resolveSnapshot is a snapshot named by stamp, "latest", or a file path:
// its rows and its stamp ("" for a path that is not a stamp file).
func (t *Tool) resolveSnapshot(name string) ([]Row, string, error) {
	path := name
	switch {
	case name == "latest":
		path = filepath.Join(t.InventoryDir(), Latest)
	case !strings.ContainsRune(name, filepath.Separator) && !strings.HasSuffix(name, ".jsonl"):
		path = t.snapshotPath(name)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, "", fmt.Errorf("no snapshot %q (%s)", name, t.tilde(path))
	}
	rows, err := readRows(path)
	return rows, strings.TrimSuffix(filepath.Base(path), ".jsonl"), err
}

// DiffArgs are diff's two snapshots: A and B as given, else the two newest
// (A empty when there is only one).
func (t *Tool) DiffArgs(words []string) (a, b []Row, stamp string, err error) {
	if len(words) > 2 {
		return nil, nil, "", fmt.Errorf("diff takes at most two snapshots, got %d", len(words))
	}
	stamps, err := t.snapshots()
	if err != nil {
		return nil, nil, "", err
	}
	names := append([]string{}, words...)
	if len(names) == 0 {
		switch len(stamps) {
		case 0:
			return nil, nil, "", fmt.Errorf("no snapshots in %s (run snapshot)", t.tilde(t.InventoryDir()))
		case 1:
			names = []string{"", stamps[0]}
		default:
			names = stamps[len(stamps)-2:]
		}
	}
	if len(names) == 1 {
		names = append(names, "latest")
	}
	if names[0] != "" {
		if a, _, err = t.resolveSnapshot(names[0]); err != nil {
			return nil, nil, "", err
		}
	}
	b, stamp, err = t.resolveSnapshot(names[1])
	if stamp == "latest" && len(stamps) > 0 {
		stamp = stamps[len(stamps)-1]
	}
	return a, b, stamp, err
}

// DiffCommand prints the changes between two snapshots, readable or as
// JSON Lines of change rows.
func (t *Tool) DiffCommand(words []string, jsonl, all bool) error {
	a, b, stamp, err := t.DiffArgs(words)
	if err != nil {
		return err
	}
	changes := Diff(a, b, all)
	if jsonl {
		var rows []Row
		for _, c := range changes {
			rows = append(rows, t.changeRowLine(stamp, c, b, a))
		}
		data, err := encodeRows(rows)
		if err != nil {
			return err
		}
		_, err = t.Stdout.Write(data)
		return err
	}
	for _, c := range changes {
		fmt.Fprintln(t.Stdout, readable(c))
	}
	return nil
}

// changeRowLine is a change row with its changelog line (unfolded).
func (t *Tool) changeRowLine(stamp string, c Change, cur, prev []Row) Row {
	r := changeRow(stamp, c)
	r.set("line", t.newView(prev, cur, nil).line(c))
	return r
}
