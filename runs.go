package main

import (
	"errors"
	"runtime"
	"sync"
)

// Runs are the snapshots that were written, one run row each, newest
// first: {kind: run, id: run:STAMP, stamp, time, rows, changes, added,
// changed, removed}, where the counts are the diff from the snapshot
// before it (the first run's rows are all added; a shadow counts as
// added). A run that found no change wrote no snapshot, so it has no row.
// Runs are derived from the snapshot files on each read, never stored.

// runRows are the run rows of the inventory directory's snapshots.
func (t *Tool) runRows() ([]Row, error) {
	stamps, err := t.snapshots()
	if err != nil {
		return nil, err
	}
	// Reading dominates (every snapshot is a full inventory), so the files
	// are read in parallel and diffed in order.
	snaps := make([][]Row, len(stamps))
	errs := make([]error, len(stamps))
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for i, stamp := range stamps {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			snaps[i], errs[i] = readRows(t.snapshotPath(stamp))
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(stamps))
	var prev []Row
	for i, stamp := range stamps {
		cur := snaps[i]
		changes := Diff(prev, cur, false)
		counts := map[string]int{}
		for _, c := range changes {
			verb := c.Verb
			if verb == "shadow" {
				verb = "added"
			}
			counts[verb]++
		}
		r := newRow("run", stamp)
		r["stamp"], r["time"] = stamp, stampText(stamp)
		r["rows"], r["changes"] = len(cur), len(changes)
		r["added"], r["changed"], r["removed"] = counts["added"], counts["changed"], counts["removed"]
		out = append(out, r)
		prev = cur
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
