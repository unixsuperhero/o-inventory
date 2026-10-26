package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const storyCatalog = `{"schema":1,"name":"o-story","version":"v1","state":"story.state",
 "nouns":[{"name":"story","verbs":[{"name":"list","kind":"application","rows":"story"},{"name":"show","kind":"application"},{"name":"state","kind":"application"}]},
          {"name":"edge","verbs":[{"name":"list","kind":"application","rows":"edge"}]}],
 "bindings":[],"pages":[]}`

// storyTool installs a fake o-story answering catalog, story state (from
// data/o-story.state) and story show --target ID (data/o-story.show.ID,
// with / spelled _).
func (f *fixture) storyTool(state string, shows map[string]string) {
	f.t.Helper()
	f.write(filepath.Join(f.data, "o-story.catalog"), storyCatalog)
	f.write(filepath.Join(f.data, "o-story.state"), state)
	for id, body := range shows {
		f.write(filepath.Join(f.data, "o-story.show."+strings.ReplaceAll(id, "/", "_")), body)
	}
	d := f.data
	script := "#!/bin/sh\ncase \"$1 $2\" in\n" +
		"  catalog*) /bin/cat '" + filepath.Join(d, "o-story.catalog") + "' ;;\n" +
		"  'story state') /bin/cat '" + filepath.Join(d, "o-story.state") + "' ;;\n" +
		"  'story show') [ \"$3\" = --target ] || exit 2; /bin/cat \"" + d + "/o-story.show.$(echo \"$4\" | /usr/bin/tr / _)\" ;;\n" +
		"  *) echo \"unknown command $1\" >&2; exit 1 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(f.bin, "o-story"), []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

const storyState = `{"noun":"story","id":"motions/spill","label":"spill: j at the last row","data":{"area":"motions","size":"80x24","chrome":"full","steps":2,"skipped":false}}
{"noun":"story","id":"chrome/wide","label":"wide footer","data":{"area":"chrome","size":"120x24","chrome":"full","steps":1,"skipped":true}}
{"noun":"edge","id":"edge/normal/insert/input.edit","label":"normal → insert via input.edit","data":{"from":"normal","to":"insert","via":"input.edit"}}
{"noun":"verb","id":"verb/normal/list.move","label":"normal list list.move","data":{"mode":"normal","noun":"list","verb":"list.move"}}
{"noun":"recording","id":"recording/1","label":"history 1: spill","data":{"ref":"1"}}
`

func TestSnapshotStoriesAndModeEdgesFromOStoryState(t *testing.T) {
	f := newFixture(t)
	f.storyTool(storyState, map[string]string{"chrome/wide": `{"name":"wide footer","area":"chrome","skip":"gap: the footer cuts at 80"}`})
	f.mustRun("snapshot")
	m := byID(f.latest())
	spill := m["story:motions/spill"]
	if spill.Str("area") != "motions" || spill.Str("name") != "spill: j at the last row" || spill.Str("file") != "stories/motions/spill.json" || spill["skip"] != nil {
		t.Fatalf("running story %v", spill)
	}
	if wide := m["story:chrome/wide"]; wide.Str("skip") != "gap: the footer cuts at 80" || wide.Str("file") != "stories/chrome/wide.json" {
		t.Fatalf("skipped story %v", wide)
	}
	if e := m["edge:normal/insert/input.edit"]; e.Str("from") != "normal" || e.Str("to") != "insert" || e.Str("via") != "input.edit" {
		t.Fatalf("edge %v", e)
	}
	for id := range m {
		if strings.HasPrefix(id, "recording:") || strings.HasPrefix(id, "verb:verb/") {
			t.Fatalf("o-story's %s recorded; only stories and edges are", id)
		}
	}

	// The running story becomes a known gap; one edge goes, one comes.
	state := strings.Replace(storyState, `"steps":2,"skipped":false`, `"steps":2,"skipped":true`, 1)
	state = strings.Replace(state, "edge/normal/insert/input.edit", "edge/normal/terminal/demo.open", 1)
	state = strings.Replace(state, `"to":"insert","via":"input.edit"`, `"to":"terminal","via":"demo.open"`, 1)
	f.storyTool(state, map[string]string{"motions/spill": `{"skip":"gap: spill wraps"}`})
	f.clock = f.clock.Add(3600e9)
	f.mustRun("snapshot")
	f.mustRun("changelog")
	data, err := os.ReadFile(filepath.Join(f.home, "hub", "CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	newest := text[strings.Index(text, "## 2026-10-24 21:10"):strings.Index(text, "## 2026-10-24 20:10")]
	want := "## 2026-10-24 21:10\n\n" +
		"### Stories and mode edges\n" +
		"- removed: edge `normal` → `insert` via `input.edit`\n" +
		"- added: edge `normal` → `terminal` via `demo.open`\n" +
		"- changed: story `motions/spill` skipped, \"gap: spill wraps\"; was running\n\n"
	if newest != want {
		t.Fatalf("newest entry:\n%q\nwant:\n%q", newest, want)
	}
	if first := text[strings.Index(text, "## 2026-10-24 20:10"):]; !strings.Contains(first, "- added: story `chrome/wide` `wide footer`, skipped \"gap: the footer cuts at 80\"\n") {
		t.Fatalf("first entry lacks the skipped story:\n%s", first)
	}
}

// A skipped story whose reason cannot be read is still skipped, with a
// warning.
func TestSnapshotSkippedStoryWithoutAReason(t *testing.T) {
	f := newFixture(t)
	f.storyTool(storyState, nil)
	f.mustRun("snapshot")
	if wide := byID(f.latest())["story:chrome/wide"]; wide.Str("skip") != "skipped" {
		t.Fatalf("skipped story %v", wide)
	}
	if !strings.Contains(f.err.String(), "chrome/wide is skipped, reason unread") {
		t.Fatalf("stderr %q", f.err.String())
	}
}
