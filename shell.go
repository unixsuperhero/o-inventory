package main

import (
	"bytes"
	"context"
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
// change rows are the diff of the previous snapshot and the latest; run
// rows are the snapshots written (runs.go).

// Version is the catalog's version.
const Version = "0.1.0"

// rowNouns are the row kinds the runtime lists.
var rowNouns = []string{"lib", "dep", "tool", "noun", "verb", "binding", "page", "host", "project", "story", "edge", "table", "change", "run"}

// ListRow is one element of a list.
type ListRow struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Data  Row    `json:"data"`
	// Cells are the row's table cells, one per column its noun declares
	// (table.go).
	Cells []string `json:"cells,omitempty"`
}

// label is how a list shows a row.
func label(r Row) string {
	tool, noun := r.Str("tool"), r.Str("noun")
	switch r.Kind() {
	case "lib":
		s := strings.TrimSpace(r.Str("name") + " " + r.Str("commit"))
		if v := r.Str("version"); v != "" && v != r.Str("commit") {
			s += " " + v
		}
		if r["dirty"] == true {
			s += ", dirty"
		}
		return s
	case "dep":
		s := r.Str("tool") + " → " + r.Str("lib") + " " + r.Str("mode")
		if v := r.Str("replace"); v != "" {
			return s + " " + v
		}
		return strings.TrimSpace(s + " " + r.Str("version"))
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
		if r["reachable"] == false {
			s += ", not reachable through o"
		}
		if r["root"] == true {
			s += ", root"
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
	case "story":
		s := strings.TrimPrefix(r.ID(), "story:") + ": " + r.Str("name")
		if skip := r.Str("skip"); skip != "" {
			s += " (skipped: " + skip + ")"
		}
		return s
	case "edge":
		return r.Str("from") + " → " + r.Str("to") + " via " + r.Str("via")
	case "table":
		s := r.Str("tool") + " " + r.Str("name") + " (" + r.Str("owner") + ", key " + r.Str("key") + ", v" + canon(r["version"])
		if r["appendOnly"] == true {
			s += ", append-only"
		}
		return s + ")"
	case "run":
		return fmt.Sprintf("%s: %s rows, %s changes", r.Str("stamp"), number(r["rows"]), number(r["changes"]))
	case "change":
		if l := r.Str("line"); l != "" {
			return l
		}
		return r.Str("verb") + " " + r.Str("row")
	}
	return r.ID()
}

// inventoryRows are the latest snapshot's rows plus its change rows (the
// diff from the snapshot before it), by kind; the run rows are read on
// their own (runRows), only when asked for.
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
	runsRead := false
	// load is the rows of the latest snapshot's nouns, and the run rows
	// too when runs is set: they read every snapshot, so a list of another
	// noun does not pay for them.
	load := func(runs bool) (map[string][]Row, error) {
		if cache != nil && (runsRead || !runs) {
			return cache, nil
		}
		rows := cache
		if rows == nil {
			var err error
			if rows, err = t.inventoryRows(); err != nil {
				return nil, err
			}
		}
		if runs {
			var err error
			if rows["run"], err = t.runRows(); err != nil {
				return nil, err
			}
			runsRead = true
		}
		for _, noun := range rowNouns {
			items := make([]ochazuke.ListItem, len(rows[noun]))
			for i, row := range rows[noun] {
				items[i] = ochazuke.ListItem{ID: row.ID(), Label: label(row), Noun: noun, Data: row, Cells: cellsOf(noun, row)}
			}
			if err := lists[noun].SetItems(items); err != nil {
				return nil, err
			}
		}
		cache = rows
		return rows, nil
	}
	listed := func(noun string) ochazuke.Result {
		rows, err := load(noun == "run")
		if err != nil {
			return ochazuke.Failed(err)
		}
		out := make([]ListRow, len(rows[noun]))
		for i, row := range rows[noun] {
			out[i] = ListRow{ID: row.ID(), Label: label(row), Data: row, Cells: cellsOf(noun, row)}
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
		rows, err := load(noun == "run")
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
		listDesc := "the latest snapshot's " + noun + " rows: {id, label, data: the row, cells}"
		if noun == "run" {
			listDesc = "the snapshots written, newest first: {id, label, data: {stamp, time, rows, changes, added, changed, removed}, cells}"
		}
		r.Define(noun).
			Verb("list", func(ochazuke.Ctx) ochazuke.Result { return listed(noun) }, listDesc).Rows(noun).
			Verb("show", func(c ochazuke.Ctx) ochazuke.Result { return show(noun, c) },
				"one "+noun+" row: ENTRY (an id or label) or --target ID").Positional("entry")
		op := r.Operations[noun+".show"]
		op.Parameters = map[string]ochazuke.Parameter{"entry": {Type: "string", Description: "an id (" + noun + ":...) or label from " + noun + " list"}}
		r.Operations[noun+".show"] = op
		if tb, ok := tables[noun]; ok { // the catalog's columns, and the list drawn as a table
			r.Define(noun).Columns(tb.columns...)
			lists[noun].SetColumns(tb.columns...)
		}
	}
	r.Define("inventory").
		Verb("state", func(ochazuke.Ctx) ochazuke.Result {
			rows, err := load(true)
			if err != nil {
				return ochazuke.Failed(err)
			}
			var out ocli.Lines
			for _, noun := range rowNouns {
				for _, row := range rows[noun] {
					out = append(out, ocli.Row{Noun: noun, ID: row.ID(), Label: label(row), Cells: cellsOf(noun, row)})
				}
			}
			return ochazuke.Done(out)
		}, "every list's rows in one answer, JSON Lines {noun, id, label, cells} for "+strings.Join(rowNouns, ", ")+" (the catalog's state)").State().
		Verb("overview", func(c ochazuke.Ctx) ochazuke.Result {
			ov, err := t.Overview()
			if err != nil {
				return ochazuke.Failed(err)
			}
			if asJSON, _ := c.Args["json"].(bool); asJSON {
				return ochazuke.Done(ov)
			}
			var out ocli.Lines
			for _, line := range strings.Split(strings.TrimRight(ov.Text(), "\n"), "\n") {
				out = append(out, line)
			}
			return ochazuke.Done(out)
		}, "the latest snapshot as one tree: libs, root, tools, hosts, projects (--json: the tree as data)")
	{
		op := r.Operations["inventory.overview"]
		op.Parameters = map[string]ochazuke.Parameter{"json": {Type: "boolean", Description: "the tree as data instead of text lines"}}
		r.Operations["inventory.overview"] = op
	}

	daily := r.Define("daily")
	for _, v := range dailyVerbs {
		verb := v.Name
		daily.Verb(verb, func(c ochazuke.Ctx) ochazuke.Result {
			o := DailyOptions{Out: ochazuke.StringArg(c.Args, "out", ""), Hour: ochazuke.IntArg(c.Args, "hour", 9), Minute: ochazuke.IntArg(c.Args, "minute", 0)}
			var buf bytes.Buffer
			if err := t.Daily(context.Background(), verb, o, &buf); err != nil {
				return ochazuke.Failed(err)
			}
			var out ocli.Lines
			for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
				out = append(out, line)
			}
			return ochazuke.Done(out)
		}, v.Summary)
	}
	timeParams := map[string]ochazuke.Parameter{
		"hour":   {Type: "integer", Description: "the hour the job runs, local time (default 9)"},
		"minute": {Type: "integer", Description: "the minute the job runs (default 0)"},
	}
	for _, verb := range []string{"plist", "install"} {
		op := r.Operations["daily."+verb]
		op.Parameters = map[string]ochazuke.Parameter{}
		for k, p := range timeParams {
			op.Parameters[k] = p
		}
		if verb == "plist" {
			op.Parameters["out"] = ochazuke.Parameter{Type: "string", Description: "write the plist to this path instead of stdout"}
		}
		r.Operations["daily."+verb] = op
	}

	sh := ocli.New(r, "o-inventory", t.Stdin, t.Stdout, t.Stderr)
	sh.Usage = ocli.DefaultUsage(t.Prog)
	sh.Version = Version
	sh.Reveal = func(target string) error {
		kind, _, ok := strings.Cut(target, ":")
		if _, err := load(kind == "run"); err != nil {
			return err
		}
		if !ok || lists[kind] == nil {
			return errors.New("not a row id (KIND:PATH from a list): " + target)
		}
		return nil
	}
	return sh
}
