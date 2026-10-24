package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

// Row is one fact: {kind, id, ...fields}. id is "<kind>:<path>" and stable
// across snapshots; the fields are what the source said. Rows are written
// as JSON objects, whose keys encoding/json sorts, one per line, by id.
type Row map[string]any

// Kinds are the row kinds a snapshot holds, in the order the changelog's
// concepts read them.
var Kinds = []string{"tool", "noun", "verb", "param", "binding", "page", "host", "project", "story", "edge", "table"}

func (r Row) ID() string   { s, _ := r["id"].(string); return s }
func (r Row) Kind() string { s, _ := r["kind"].(string); return s }

// Str is a string field, "" when absent or not a string.
func (r Row) Str(key string) string { s, _ := r[key].(string); return s }

// set adds key unless v is a zero value (empty string, false, nil, empty
// slice or map), so rows stay small and absent equals empty.
func (r Row) set(key string, v any) Row {
	if !zero(v) {
		r[key] = v
	}
	return r
}

func zero(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map:
		return rv.Len() == 0
	}
	return rv.IsZero()
}

func newRow(kind, path string) Row { return Row{"kind": kind, "id": kind + ":" + path} }

// canon is v's JSON, the form fields are compared in: a number read back
// from a file (float64) equals the int it was written from.
func canon(v any) string {
	if zero(v) {
		return "null"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var back any
	if json.Unmarshal(b, &back) == nil && zero(back) {
		return "null"
	}
	return string(b)
}

// sortRows orders rows by id.
func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID() < rows[j].ID() })
}

// encodeRows is rows as JSON Lines, sorted by id.
func encodeRows(rows []Row) ([]byte, error) {
	sorted := append([]Row(nil), rows...)
	sortRows(sorted)
	var buf bytes.Buffer
	for _, r := range sorted {
		b, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// readRows is a snapshot file's rows; a missing file is no rows.
func readRows(path string) ([]Row, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var rows []Row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Row
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		rows = append(rows, r)
	}
	return rows, sc.Err()
}

// byID indexes rows by id.
func byID(rows []Row) map[string]Row {
	m := make(map[string]Row, len(rows))
	for _, r := range rows {
		m[r.ID()] = r
	}
	return m
}
