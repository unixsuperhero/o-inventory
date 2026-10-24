package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Concepts are the changelog's ### sections, in order, and the row kinds
// each one reads. Commits is last and comes from git, not the diff.
var Concepts = []struct {
	Title string
	Kinds []string
}{
	{"Root and libraries", []string{"lib", "dep", "shadow"}},
	{"Tools", []string{"tool"}},
	{"Nouns and verbs", []string{"noun", "verb", "param"}},
	{"Keybindings", []string{"binding"}},
	{"Pages", []string{"page"}},
	{"Hosts", []string{"host"}},
	{"Projects", []string{"project"}},
	{"Stories and mode edges", []string{"story", "edge"}},
	{"Store", []string{"table"}},
}

// ChangelogEntry is one snapshot's entry: ## header, then sections.
type ChangelogEntry struct {
	Stamp  string
	Header string
	Text   string // the whole entry, header included, ending in a newline
}

// view is what rendering a diff reads: both snapshots by id, the repo of
// each tool, and the folds (a new tool's or noun's rows told in its line).
type view struct {
	t        *Tool
	cur, old map[string]Row
	toolRepo map[string]string
	repoSet  map[string]bool
	folded   map[string]bool           // change ids told in another line
	counts   map[string]map[string]int // tool or noun id -> kind -> folded count
	verbsOf  map[string][]string       // noun id -> folded verb names
	paramsOf map[string][]string       // verb id -> folded param names
	tables   []tableLine               // whole key tables added or removed
}

// tableLine is one line for a noun's whole binding table, added or removed
// at once (a noun with bindings and no verbs, the framework's editor).
type tableLine struct {
	tool, text string
}

func (t *Tool) newView(prev, cur []Row, repos []Repo) *view {
	v := &view{t: t, cur: byID(cur), old: byID(prev), toolRepo: map[string]string{Framework: Framework}, repoSet: map[string]bool{},
		folded: map[string]bool{}, counts: map[string]map[string]int{}, verbsOf: map[string][]string{}, paramsOf: map[string][]string{}}
	for _, rows := range [][]Row{prev, cur} {
		for _, r := range rows {
			if r.Kind() == "tool" {
				v.toolRepo[r.Str("name")] = r.Str("repo")
			}
		}
	}
	for _, r := range repos {
		v.repoSet[r.Name] = true
	}
	return v
}

// fold marks the rows a new (or removed) tool, noun or verb line tells.
func (v *view) fold(changes []Change) {
	same := map[string]string{} // container id -> verb of its change
	for _, c := range changes {
		if c.Verb == "changed" {
			continue
		}
		r := c.Row()
		switch c.Kind {
		case "tool":
			if r["catalog"] == true {
				same[c.ID] = c.Verb
			}
		case "noun", "verb":
			same[c.ID] = c.Verb
		}
	}
	count := func(container, kind string) {
		if v.counts[container] == nil {
			v.counts[container] = map[string]int{}
		}
		v.counts[container][kind]++
	}
	for _, c := range changes {
		if c.Verb == "changed" {
			continue
		}
		r := c.Row()
		tool, noun := r.Str("tool"), r.Str("noun")
		toolID := "tool:" + tool
		nounID := "noun:" + tool + "/" + noun
		verbID := "verb:" + tool + "/" + noun + "." + r.Str("verb")
		switch c.Kind {
		case "noun", "verb", "param", "binding", "page":
			if tool != Framework && same[toolID] == c.Verb {
				v.folded[c.ID] = true
				count(toolID, c.Kind)
				continue
			}
		}
		switch c.Kind {
		case "verb":
			if same[nounID] == c.Verb {
				v.folded[c.ID] = true
				v.verbsOf[nounID] = append(v.verbsOf[nounID], r.Str("verb"))
			}
		case "param":
			if same[verbID] == c.Verb {
				v.folded[c.ID] = true
				v.paramsOf[verbID] = append(v.paramsOf[verbID], r.Str("param"))
			} else if same[nounID] == c.Verb {
				v.folded[c.ID] = true
			}
		case "binding":
			if same[nounID] == c.Verb {
				v.folded[c.ID] = true
				count(nounID, "binding")
			}
		}
	}
	v.foldTables(changes)
}

// foldTables tells the bindings of a tool/noun that had none before (or has
// none now) in one line, unless a noun line already tells them.
func (v *view) foldTables(changes []Change) {
	type group struct {
		ids   []string
		modes map[string]bool
	}
	groups := map[string]*group{}
	var keys []string
	for _, c := range changes {
		if c.Kind != "binding" || c.Verb == "changed" || v.folded[c.ID] {
			continue
		}
		r := c.Row()
		key := c.Verb + " " + r.Str("tool") + " " + r.Str("noun")
		if groups[key] == nil {
			groups[key] = &group{modes: map[string]bool{}}
			keys = append(keys, key)
		}
		groups[key].ids = append(groups[key].ids, c.ID)
		groups[key].modes[r.Str("mode")] = true
	}
	remaining := func(rows map[string]Row, tool, noun string) int {
		n := 0
		for _, r := range rows {
			if r.Kind() == "binding" && r.Str("tool") == tool && r.Str("noun") == noun {
				n++
			}
		}
		return n
	}
	sort.Strings(keys)
	for _, key := range keys {
		g := groups[key]
		parts := strings.SplitN(key, " ", 3)
		verb, tool, noun := parts[0], parts[1], parts[2]
		other := v.old // added: the table was empty before
		if verb == "removed" {
			other = v.cur // removed: the table is empty now
		}
		if len(g.ids) < 2 || remaining(other, tool, noun) > 0 {
			continue
		}
		var modes []string
		for m := range g.modes {
			modes = append(modes, m)
		}
		for _, id := range g.ids {
			v.folded[id] = true
		}
		v.tables = append(v.tables, tableLine{tool: tool, text: verb + ": `" + tool + " " + noun + "` key table, " + plural(len(g.ids), "binding") + " in modes " + quoteList(modes, "")})
	}
}

// repoOf is the family repo a row belongs to: a tool's repo, the repo of
// the tool a noun, verb, param, binding or page belongs to (ochazuke for
// the framework's), a host's mounted bin's tool's, a project's own name
// when it is a family repo, o-story's for stories and edges.
func (v *view) repoOf(r Row) string {
	switch r.Kind() {
	case "tool":
		return r.Str("repo")
	case "host":
		for _, rows := range []map[string]Row{v.cur, v.old} {
			for _, t := range rows {
				if t.Kind() == "tool" && t.Str("bin") == r.Str("bin") {
					return t.Str("repo")
				}
			}
		}
		return ""
	case "project":
		if v.repoSet[r.Str("name")] {
			return r.Str("name")
		}
		return ""
	case "story", "edge":
		return v.toolRepo["o-story"]
	case "lib":
		return r.Str("name")
	case "dep":
		if r.Str("consumer") == "lib" {
			return r.Str("tool")
		}
	}
	return v.toolRepo[r.Str("tool")]
}

// val is a field value as the changelog spells it: none, a number, yes,
// or `text`.
func val(x any) string {
	switch x := x.(type) {
	case float64:
		return fmt.Sprintf("%g", x)
	case int:
		return fmt.Sprint(x)
	}
	if zero(x) {
		return "none"
	}
	switch x := x.(type) {
	case string:
		return "`" + x + "`"
	case bool:
		return "yes"
	case []any, []string:
		s := canon(x)
		s = strings.NewReplacer(`"`, "", "[", "", "]", "").Replace(s)
		return "`" + strings.ReplaceAll(s, ",", ", ") + "`"
	}
	return "`" + canon(x) + "`"
}

func quoteList(names []string, prefix string) string {
	sort.Strings(names)
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "`" + prefix + n + "`"
	}
	return strings.Join(out, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	if strings.HasSuffix(word, "s") {
		return fmt.Sprintf("%d %ses", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// thing is how a line names a row.
func (v *view) thing(r Row) string {
	tool, noun := r.Str("tool"), r.Str("noun")
	switch r.Kind() {
	case "tool":
		return "`" + r.Str("name") + "`"
	case "noun":
		return "noun `" + tool + " " + noun + "`"
	case "verb":
		return "`" + tool + " " + noun + " " + r.Str("verb") + "`"
	case "param":
		return "`--" + r.Str("param") + "` on `" + tool + " " + noun + " " + r.Str("verb") + "`"
	case "binding":
		keys := r.Str("keys")
		if keys == "" {
			keys = "pattern " + r.Str("pattern")
		}
		return "`" + keys + "` in " + tool + " " + modeName(r.Str("mode"))
	case "page":
		return "page `" + r.Str("page") + "` in " + tool
	case "host":
		return "nabe `" + r.Str("name") + "`"
	case "project":
		if r["registered"] != true {
			return "hub link `" + r.Str("name") + "`"
		}
		return "project `" + r.Str("name") + "`"
	case "story":
		return "story `" + strings.TrimPrefix(r.ID(), "story:") + "`"
	case "edge":
		return "edge `" + r.Str("from") + "` → `" + r.Str("to") + "` via `" + r.Str("via") + "`"
	case "table":
		return "table `" + strings.TrimPrefix(r.ID(), "table:") + "`"
	case "lib":
		return "`" + r.Str("name") + "`"
	case "dep":
		return "`" + tool + "` → `" + r.Str("lib") + "`"
	case "shadow":
		return "`" + tool + " " + r.Str("name") + "`"
	}
	return "`" + r.ID() + "`"
}

// lines are a change's changelog lines: one for an added or removed row,
// one per field for a changed one; none when another line tells it.
func (v *view) lines(c Change) []string {
	if v.folded[c.ID] {
		return nil
	}
	r := c.Row()
	switch c.Verb {
	case "added":
		return []string{"added: " + v.addedText(r)}
	case "removed":
		return []string{"removed: " + v.removedText(r)}
	case "shadow":
		return []string{"shadow: " + v.thing(r) + " runs `" + r.Str("winner") + "`; " + r.Str("claimKind") + " `" + r.Str("source") + "` is shadowed"}
	}
	var out []string
	for _, f := range c.Fields {
		line := "changed: " + v.changedText(r, f)
		if r.Kind() == "lib" && f.Field == "commit" && c.Old != nil && c.Old.Str("sourceCommit") != "" && c.Old.Str("sourceCommit") == r.Str("sourceCommit") {
			line += "; source unchanged"
		}
		out = append(out, line)
	}
	return out
}

// counted is a tool's or noun's folded rows: ", 3 nouns, 12 verbs, ...".
func (v *view) counted(id string) string {
	var parts []string
	for _, k := range []string{"noun", "verb", "param", "binding", "page"} {
		if n := v.counts[id][k]; n > 0 {
			parts = append(parts, plural(n, k))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return ", " + strings.Join(parts, ", ")
}

func (v *view) addedText(r Row) string {
	switch r.Kind() {
	case "lib":
		s := "library `" + r.Str("name") + "` at `" + r.Str("path") + "`"
		if c := strings.TrimSpace(r.Str("commit") + " " + r.Str("version")); c != "" {
			s += " (" + c + ")"
		}
		return s
	case "dep":
		s := v.thing(r) + " by " + r.Str("mode")
		if r.Str("replace") != "" {
			s += " `" + r.Str("replace") + "`"
		} else if r.Str("version") != "" {
			s += " `" + r.Str("version") + "`"
		}
		return s
	case "tool":
		s := "`" + r.Str("name") + "` at `" + r.Str("bin") + "`"
		if repo := strings.TrimSpace(r.Str("repo") + " " + r.Str("commit")); repo != "" {
			s += " (" + repo + ")"
		}
		switch {
		case r.Str("alias") != "":
			s += ", the same binary as `" + r.Str("alias") + "`"
		case r["catalog"] != true:
			s += ", no catalog"
		}
		return s + v.counted(r.ID())
	case "noun":
		s := v.thing(r)
		if verbs := v.verbsOf[r.ID()]; len(verbs) > 0 {
			s += " with verbs " + quoteList(verbs, "")
		}
		if r.Str("pin") != "" {
			s += ", pinned to mode `" + r.Str("pin") + "`"
		}
		return s + v.counted(r.ID())
	case "verb":
		s := v.thing(r)
		if params := v.paramsOf[r.ID()]; len(params) > 0 {
			s += ", params " + quoteList(params, "--")
		}
		if r.Str("rows") != "" {
			s += ", rows `" + r.Str("rows") + "`"
		}
		if r.Str("confirm") != "" {
			s += ", confirms"
		}
		return s
	case "param":
		s := v.thing(r)
		if r.Str("type") != "" {
			s += " (" + r.Str("type") + ")"
		}
		return s
	case "binding":
		return v.thing(r) + ", `" + r.Str("request") + "`"
	case "page":
		return v.thing(r) + ", title " + val(r["title"])
	case "host":
		return "nabe page `" + r.Str("name") + "` for `" + r.Str("bin") + "`, order " + val(r["order"])
	case "project":
		if r["registered"] != true {
			return v.thing(r) + " to `" + r.Str("path") + "`"
		}
		s := v.thing(r)
		if r.Str("origin") != "" {
			s += " (" + r.Str("origin") + ")"
		}
		if r.Str("path") != "" {
			s += " at `" + r.Str("path") + "`"
		}
		if r["hubLink"] == true {
			s += ", hub link"
		}
		return s
	}
	return v.thing(r)
}

func (v *view) removedText(r Row) string {
	s := v.thing(r)
	switch r.Kind() {
	case "tool":
		s += " from `" + r.Str("bin") + "`" + v.counted(r.ID())
	case "noun":
		if verbs := v.verbsOf[r.ID()]; len(verbs) > 0 {
			s += " and its verbs " + quoteList(verbs, "")
		}
		s += v.counted(r.ID())
	case "verb":
		if params := v.paramsOf[r.ID()]; len(params) > 0 {
			s += " and its params " + quoteList(params, "--")
		}
	}
	return s
}

func (v *view) changedText(r Row, f FieldChange) string {
	switch {
	case r.Kind() == "binding" && f.Field == "request":
		return v.thing(r) + ", " + val(f.Now) + "; was " + val(f.Was)
	case r.Kind() == "story" && f.Field == "skip":
		state := func(x any) string {
			if zero(x) {
				return "running"
			}
			return fmt.Sprintf("skipped, %q", x)
		}
		return v.thing(r) + " " + state(f.Now) + "; was " + state(f.Was)
	}
	return v.thing(r) + " " + f.Field + " " + val(f.Now) + "; was " + val(f.Was)
}

// line is one change's line for a change row (unfolded).
func (v *view) line(c Change) string {
	if l := v.lines(c); len(l) > 0 {
		return strings.Join(l, " / ")
	}
	return ""
}

// Commit is one commit subject in a repo.
type Commit struct {
	Repo, Hash, Subject string
}

// commits are each repo's commits after since and up to until (by commit
// time), newest first; repos that are not repositories are skipped.
func commitsBetween(repos []Repo, since, until time.Time, home func(string) string) []Commit {
	var out []Commit
	for _, r := range repos {
		dir := home(r.Path)
		if !isRepoRoot(dir) {
			continue
		}
		log, err := git(dir, "log", "--no-merges", "--format=%h%x09%s",
			"--since="+since.Add(time.Second).UTC().Format(time.RFC3339), "--until="+until.UTC().Format(time.RFC3339))
		if err != nil || log == "" {
			continue
		}
		for _, line := range strings.Split(log, "\n") {
			hash, subject, _ := strings.Cut(line, "\t")
			out = append(out, Commit{Repo: r.Name, Hash: hash, Subject: subject})
		}
	}
	return out
}

// fixSubject is a fix: or fix(scope): subject without its prefix.
func fixSubject(subject string) (string, bool) {
	rest, ok := strings.CutPrefix(subject, "fix")
	if !ok {
		return "", false
	}
	if strings.HasPrefix(rest, "(") {
		i := strings.Index(rest, ")")
		if i < 0 {
			return "", false
		}
		rest = rest[i+1:]
	}
	rest = strings.TrimPrefix(rest, "!")
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	return strings.TrimSpace(rest[1:]), true
}

func stampTime(stamp string) time.Time {
	t, _ := time.Parse(StampLayout, stamp)
	return t
}

// Entries are the changelog's entries, newest first: one per snapshot
// whose diff against the one before (or commits since it) has a line;
// repo, when set, keeps only that repo's lines.
func (t *Tool) Entries(repo string) ([]ChangelogEntry, error) {
	stamps, err := t.snapshots()
	if err != nil {
		return nil, err
	}
	repos, _, err := t.Repos()
	if err != nil {
		return nil, err
	}
	minutes := map[string]bool{} // minutes an earlier snapshot's header took
	var entries []ChangelogEntry
	var prev []Row
	for i, stamp := range stamps {
		cur, err := readRows(t.snapshotPath(stamp))
		if err != nil {
			return nil, err
		}
		changes := Diff(prev, cur, false)
		v := t.newView(prev, cur, repos)
		v.fold(changes)
		sections := map[string][]string{}
		for _, c := range changes {
			if repo != "" && v.repoOf(c.Row()) != repo {
				continue
			}
			for _, k := range Concepts {
				for _, kind := range k.Kinds {
					if kind == c.Kind {
						sections[k.Title] = append(sections[k.Title], v.lines(c)...)
					}
				}
			}
		}
		for _, tl := range v.tables {
			if repo == "" || v.toolRepo[tl.tool] == repo {
				sections["Keybindings"] = append(sections["Keybindings"], tl.text)
			}
		}
		if i > 0 {
			var fixed, plain []string
			for _, c := range commitsBetween(repos, stampTime(stamps[i-1]), stampTime(stamp), t.untilde) {
				if repo != "" && c.Repo != repo {
					continue
				}
				if s, ok := fixSubject(c.Subject); ok {
					fixed = append(fixed, "fixed: "+s+" ("+c.Repo+" "+c.Hash+")")
				} else {
					plain = append(plain, c.Repo+": "+c.Hash+" "+c.Subject)
				}
			}
			sections["Commits"] = append(fixed, plain...)
		}
		prev = cur
		// The header is the minute; a later snapshot in a minute already
		// taken adds seconds, so no header changes once written.
		minute := stampTime(stamp).Format("2006-01-02 15:04")
		header := "## " + minute
		if minutes[minute] {
			header = "## " + stampTime(stamp).Format("2006-01-02 15:04:05")
		}
		minutes[minute] = true
		var b strings.Builder
		b.WriteString(header + "\n")
		titles := []string{}
		for _, k := range Concepts {
			titles = append(titles, k.Title)
		}
		any := false
		for _, title := range append(titles, "Commits") {
			if len(sections[title]) == 0 {
				continue
			}
			any = true
			b.WriteString("\n### " + title + "\n")
			for _, l := range sections[title] {
				b.WriteString("- " + l + "\n")
			}
		}
		if any {
			entries = append(entries, ChangelogEntry{Stamp: stamp, Header: header, Text: b.String()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Stamp > entries[j].Stamp })
	return entries, nil
}

// changelogPreamble starts a changelog file the tool creates.
const changelogPreamble = "# Changelog\n\nGenerated by o-inventory from the snapshots in ~/hub/inventory: one entry per snapshot, newest first, only the concepts that changed. Every line but Commits is derived from the diff; `fixed:` lines quote `fix:` commit subjects.\n"

// mergeChangelog is existing with the entries whose header it lacks
// prepended (before its first ## heading), newest first; a new file starts
// with the preamble.
func mergeChangelog(existing string, entries []ChangelogEntry) (string, int) {
	if strings.TrimSpace(existing) == "" {
		existing = changelogPreamble
	}
	present := map[string]bool{}
	for _, line := range strings.Split(existing, "\n") {
		if strings.HasPrefix(line, "## ") {
			present[strings.TrimSpace(line)] = true
		}
	}
	var add []string
	for _, e := range entries {
		if !present[e.Header] {
			add = append(add, e.Text)
		}
	}
	if len(add) == 0 {
		return existing, 0
	}
	block := strings.Join(add, "\n") + "\n"
	idx := -1
	if strings.HasPrefix(existing, "## ") {
		idx = 0
	} else if i := strings.Index(existing, "\n## "); i >= 0 {
		idx = i + 1
	}
	if idx < 0 {
		return strings.TrimRight(existing, "\n") + "\n\n" + strings.TrimRight(block, "\n") + "\n", len(add)
	}
	head := existing[:idx]
	if !strings.HasSuffix(head, "\n\n") && head != "" {
		head = strings.TrimRight(head, "\n") + "\n\n"
	}
	return head + block + existing[idx:], len(add)
}

// writeChangelog merges entries into path; it reports how many were new.
func writeChangelog(path string, entries []ChangelogEntry) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	merged, n := mergeChangelog(string(data), entries)
	if n == 0 {
		return 0, nil
	}
	return n, writeFile(path, []byte(merged))
}

// ChangelogOptions are changelog's flags.
type ChangelogOptions struct {
	Repo       string
	WriteRepos bool
	Commit     bool
	Quiet      bool
}

// Changelog writes ~/hub/CHANGELOG.md (merging new entries), or prints one
// repo's entries with Repo; WriteRepos also merges each family repo's
// entries into its CHANGELOG.md (only repos with entries; created when
// missing), which it never commits: they are left for the owner to
// review. Commit commits the hub's file and nothing outside the hub.
func (t *Tool) Changelog(_ context.Context, o ChangelogOptions) error {
	if o.Repo != "" && !o.WriteRepos {
		entries, err := t.Entries(o.Repo)
		if err != nil {
			return err
		}
		merged, _ := mergeChangelog("", entries)
		_, err = fmt.Fprint(t.Stdout, merged)
		return err
	}
	entries, err := t.Entries("")
	if err != nil {
		return err
	}
	hubFile := filepath.Join(t.Hub(), "CHANGELOG.md")
	n, err := writeChangelog(hubFile, entries)
	if err != nil {
		return err
	}
	say(t.Stdout, o.Quiet, "%s: %d entries, %d new", t.tilde(hubFile), len(entries), n)
	if o.Commit && n > 0 {
		msg := "changelog: " + entries[0].Stamp
		if err := gitCommit(t.Hub(), msg, hubFile); err != nil {
			return err
		}
		say(t.Stdout, o.Quiet, "committed in %s: %s", t.tilde(t.Hub()), msg)
	}
	if !o.WriteRepos {
		return nil
	}
	repos, _, err := t.Repos()
	if err != nil {
		return err
	}
	for _, r := range repos {
		if o.Repo != "" && r.Name != o.Repo {
			continue
		}
		dir := t.untilde(r.Path)
		if !isRepoRoot(dir) {
			continue
		}
		entries, err := t.Entries(r.Name)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			continue
		}
		file := filepath.Join(dir, "CHANGELOG.md")
		n, err := writeChangelog(file, entries)
		if err != nil {
			return err
		}
		if n > 0 {
			say(t.Stdout, o.Quiet, "%s: %d new", t.tilde(file), n)
		}
	}
	return nil
}

// modeName is a binding's mode as a line names it; "" binds in every mode.
func modeName(mode string) string {
	if mode == "" {
		return "every mode"
	}
	return mode
}
