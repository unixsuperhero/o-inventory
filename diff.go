package main

import (
	"sort"
)

// compared are the fields a change is detected on, by row kind (the
// design's detection rules). Every other field is carried for reading and
// for diff --all: descriptions, registeredAt, binMtime, catalog versions a
// registry holds, row order.
var compared = map[string][]string{
	"tool":    {"bin", "version", "commit", "catalog", "alias", "state"},
	"noun":    {"pin", "framework"},
	"verb":    {"verbKind", "result", "confirm", "repeatable", "rows", "quiet", "positional"},
	"param":   {"type", "required", "enum", "source", "free"},
	"binding": {"request", "args", "when", "unbind", "replay"},
	"page":    {"title"},
	"host":    {"order", "refreshEvery", "bin"},
	"project": {"origin", "hubLink", "status", "path", "registered"},
	"story":   {"skip"},
	"edge":    {},
	"table":   {"version", "owner"},
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
	Verb   string        `json:"verb"` // added, changed, removed
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
			out = append(out, Change{Verb: "added", ID: id, Kind: r.Kind(), New: r})
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
