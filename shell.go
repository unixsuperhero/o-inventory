package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// The data side is an ochazuke runtime behind ochazuke/cli: one noun per
// row kind a host lists (rowNouns), each with list (rows of the latest
// snapshot, declared with Rows so nabe mounts them) and show (one row by
// --target ID or ENTRY), and inventory state, every list's rows as JSON
// Lines (the catalog's state). A list row is {id, label, data: the row};
// change rows are the diff of the previous snapshot and the latest.

// Version is the catalog's version.
const Version = "0.1.0"

// rowNouns are the row kinds the runtime lists.
var rowNouns = []string{"tool", "noun", "verb", "binding", "page", "host", "project", "change"}

// shellNouns are the runtime's own nouns, mounted as commands.
var shellNouns = append(append([]string{}, rowNouns...), "inventory")

// ListRow is one element of a list.
type ListRow struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Data  Row    `json:"data"`
}

// label is how a list shows a row.
func label(r Row) string {
	tool, noun := r.Str("tool"), r.Str("noun")
	switch r.Kind() {
	case "tool":
		s := r.Str("name")
		if v := r.Str("version"); v != "" {
			s += " " + v
		}
		if src := strings.TrimSpace(r.Str("repo") + " " + r.Str("commit")); src != "" {
			s += " (" + src + ")"
		}
		switch {
		case r.Str("alias") != "":
			s += ", alias of " + r.Str("alias")
		case r["catalog"] != true:
			s += ", no catalog"
		}
		return s
	case "noun":
		return tool + " " + noun
	case "verb":
		return tool + " " + noun + " " + r.Str("verb")
	case "binding":
		keys := r.Str("keys")
		if keys == "" {
			keys = "<" + r.Str("pattern") + ">"
		}
		return keys + " in " + tool + " " + modeName(r.Str("mode")) + " → " + r.Str("request")
	case "page":
		return tool + " " + r.Str("page") + ": " + r.Str("title")
	case "host":
		return "nabe " + r.Str("name") + " → " + r.Str("bin")
	case "project":
		s := r.Str("name")
		if o := r.Str("origin"); o != "" {
			s += " (" + o + ")"
		}
		return strings.TrimSpace(s + " " + r.Str("path"))
	case "change":
		if l := r.Str("line"); l != "" {
			return l
		}
		return r.Str("verb") + " " + r.Str("row")
	}
	return r.ID()
}

// inventoryRows are the latest snapshot's rows plus its change rows (the
// diff from the snapshot before it), by kind.
func (t *Tool) inventoryRows() (map[string][]Row, error) {
	out := map[string][]Row{}
	rows, err := readRows(filepath.Join(t.InventoryDir(), Latest))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Kind()] = append(out[r.Kind()], r)
	}
	stamps, err := t.snapshots()
	if err != nil || len(stamps) == 0 {
		return out, err
	}
	prev, cur, stamp, err := t.DiffArgs(nil)
	if err != nil {
		return nil, err
	}
	for _, c := range Diff(prev, cur, false) {
		out["change"] = append(out["change"], t.changeRowLine(stamp, c, cur, prev))
	}
	return out, nil
}

func (t *Tool) shell() *ocli.Shell {
	r := ochazuke.NewRuntime()
	lists := map[string]*ochazuke.List{}
	for _, noun := range rowNouns {
		list, err := r.AddList("list/"+noun, nil)
		if err != nil {
			panic(err) // a fresh runtime takes every list
		}
		lists[noun] = list
	}
	var cache map[string][]Row
	load := func() (map[string][]Row, error) {
		if cache != nil {
			return cache, nil
		}
		rows, err := t.inventoryRows()
		if err != nil {
			return nil, err
		}
		for _, noun := range rowNouns {
			items := make([]ochazuke.ListItem, len(rows[noun]))
			for i, row := range rows[noun] {
				items[i] = ochazuke.ListItem{ID: row.ID(), Label: label(row), Noun: noun, Data: row}
			}
			if err := lists[noun].SetItems(items); err != nil {
				return nil, err
			}
		}
		cache = rows
		return rows, nil
	}
	listed := func(noun string) ochazuke.Result {
		rows, err := load()
		if err != nil {
			return ochazuke.Failed(err)
		}
		out := make([]ListRow, len(rows[noun]))
		for i, row := range rows[noun] {
			out[i] = ListRow{ID: row.ID(), Label: label(row), Data: row}
		}
		return ochazuke.Done(out)
	}
	show := func(noun string, c ochazuke.Ctx) ochazuke.Result {
		if c.Instance != nil && c.Instance.Noun == noun {
			if row, ok := c.Instance.Data.(Row); ok {
				return ochazuke.Done(row)
			}
		}
		entry := ochazuke.StringArg(c.Args, "entry", "")
		if entry == "" {
			return ochazuke.Failed(fmt.Errorf("%s show needs ENTRY (an id or label from %s list) or --target ID", noun, noun))
		}
		rows, err := load()
		if err != nil {
			return ochazuke.Failed(err)
		}
		var matches []Row
		for _, row := range rows[noun] {
			if row.ID() == entry || row.ID() == noun+":"+entry || label(row) == entry {
				matches = append(matches, row)
			}
		}
		if len(matches) != 1 {
			return ochazuke.Failed(fmt.Errorf("%d %s rows match %q", len(matches), noun, entry))
		}
		return ochazuke.Done(matches[0])
	}
	for _, noun := range rowNouns {
		r.Define(noun).
			Verb("list", func(ochazuke.Ctx) ochazuke.Result { return listed(noun) },
				"the latest snapshot's "+noun+" rows: {id, label, data: the row}").Rows(noun).
			Verb("show", func(c ochazuke.Ctx) ochazuke.Result { return show(noun, c) },
				"one "+noun+" row: ENTRY (an id or label) or --target ID").Positional("entry")
		op := r.Operations[noun+".show"]
		op.Parameters = map[string]ochazuke.Parameter{"entry": {Type: "string", Description: "an id (" + noun + ":...) or label from " + noun + " list"}}
		r.Operations[noun+".show"] = op
	}
	r.Define("inventory").
		Verb("state", func(ochazuke.Ctx) ochazuke.Result {
			rows, err := load()
			if err != nil {
				return ochazuke.Failed(err)
			}
			var out ocli.Lines
			for _, noun := range rowNouns {
				for _, row := range rows[noun] {
					out = append(out, ocli.Row{Noun: noun, ID: row.ID(), Label: label(row)})
				}
			}
			return ochazuke.Done(out)
		}, "every list's rows in one answer, JSON Lines {noun, id, label} for "+strings.Join(rowNouns, ", ")+" (the catalog's state)").State()

	sh := ocli.New(r, "o-inventory", t.Stdin, t.Stdout, t.Stderr)
	sh.Usage = ocli.DefaultUsage(t.Prog)
	sh.Version = Version
	sh.Reveal = func(target string) error {
		if _, err := load(); err != nil {
			return err
		}
		kind, _, ok := strings.Cut(target, ":")
		if !ok || lists[kind] == nil {
			return errors.New("not a row id (KIND:PATH from a list): " + target)
		}
		return nil
	}
	return sh
}
