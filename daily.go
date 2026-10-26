package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/unixsuperhero/ochazuke"
)

// The daily job: a launchd agent in the gui/UID domain that runs a
// generated wrapper script at a calendar time (09:00 local by default).
// The wrapper snapshots (committing in the hub) and merges the changelogs;
// per-repo CHANGELOG.md files stay uncommitted for the owner to review.
// The verbs, under the daily noun:
//
//	daily plist [--out PATH] [--hour H --minute M]   print or write the plist (and nothing else)
//	daily install [--hour H --minute M]              wrapper, log and inventory dirs, plist, plutil -lint, bootout+bootstrap, print
//	daily uninstall                                  bootout, remove the plist and the wrapper; the logs stay
//	daily status                                     plist present, loaded, last run (the log), next run
//	daily run                                        the wrapper now, in the foreground

// DailyLabel is the launchd label.
const DailyLabel = "com.unixsuperhero.o-inventory"

// DailyJob is what the wrapper runs.
const DailyJob = "o-inventory snapshot --commit --quiet && o-inventory changelog --write-repos --commit --quiet"

// dailyVerbs are the daily noun's verbs and what each does.
// History is the verb's history kind: status only reads; plist can write
// --out, so it is a change like the verbs that install, remove or run.
var dailyVerbs = []struct{ Name, Summary, History string }{
	{"plist", "the launchd plist (" + DailyLabel + ", 09:00 local unless --hour/--minute) on stdout, or written to --out PATH", ochazuke.HistoryChange},
	{"install", "write the wrapper, the log and inventory dirs and ~/Library/LaunchAgents/" + DailyLabel + ".plist, plutil -lint it, launchctl bootstrap it in gui/UID (bootout first when loaded) and print it; idempotent", ochazuke.HistoryChange},
	{"uninstall", "launchctl bootout the job, remove the plist and the wrapper; the logs stay", ochazuke.HistoryChange},
	{"status", "whether the plist exists and launchd has it loaded, the last run (the log) and the next scheduled run", ochazuke.HistoryFetch},
	{"run", "run the wrapper now in the foreground: what the job does (snapshot --commit, changelog --write-repos --commit)", ochazuke.HistoryChange},
}

// DailyOptions are the daily verbs' flags.
type DailyOptions struct {
	Out          string
	Hour, Minute int
}

// DailyPlistPath is the agent's plist.
func (t *Tool) DailyPlistPath() string {
	return filepath.Join(t.Home, "Library", "LaunchAgents", DailyLabel+".plist")
}

// DailyScriptPath is the wrapper: $XDG_DATA_HOME/o-inventory/daily.sh, else
// ~/.local/share/o-inventory/daily.sh.
func (t *Tool) DailyScriptPath() string {
	base := t.getenv("XDG_DATA_HOME")
	if base == "" {
		base = filepath.Join(t.Home, ".local", "share")
	}
	return filepath.Join(base, "o-inventory", "daily.sh")
}

// DailyLogDir holds the run log (daily.log, one line per run, written by
// the wrapper) and launchd's stdout.log and stderr.log.
func (t *Tool) DailyLogDir() string { return filepath.Join(t.Home, "Library", "Logs", "o-inventory") }

func (t *Tool) dailyLog() string { return filepath.Join(t.DailyLogDir(), "daily.log") }

// goBin is where go install puts binaries: $GOBIN, else $GOPATH/bin, else
// ~/go/bin.
func (t *Tool) goBin() string {
	if v := t.getenv("GOBIN"); v != "" {
		return v
	}
	if v := t.getenv("GOPATH"); v != "" {
		return filepath.Join(filepath.SplitList(v)[0], "bin")
	}
	return filepath.Join(t.Home, "go", "bin")
}

// dailyEnv is the job's environment (the plist's EnvironmentVariables).
func (t *Tool) dailyEnv() [][2]string {
	path := strings.Join([]string{filepath.Join(t.Home, "bin"), t.goBin(), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin"}, ":")
	return [][2]string{{"PATH", path}, {"HOME", t.Home}, {"MTM_SERVER", "default"}}
}

func (t *Tool) domain() string  { return "gui/" + strconv.Itoa(t.UID) }
func (t *Tool) service() string { return t.domain() + "/" + DailyLabel }

// DailyPlist is the agent's plist.
func (t *Tool) DailyPlist(hour, minute int) string {
	esc := html.EscapeString
	var env strings.Builder
	for _, kv := range t.dailyEnv() {
		env.WriteString("    <key>" + esc(kv[0]) + "</key>\n    <string>" + esc(kv[1]) + "</string>\n")
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + DailyLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>` + esc(t.DailyScriptPath()) + `</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
` + env.String() + `  </dict>
  <key>StartCalendarInterval</key>
  <dict>
    <key>Hour</key>
    <integer>` + strconv.Itoa(hour) + `</integer>
    <key>Minute</key>
    <integer>` + strconv.Itoa(minute) + `</integer>
  </dict>
  <key>RunAtLoad</key>
  <false/>
  <key>StandardOutPath</key>
  <string>` + esc(filepath.Join(t.DailyLogDir(), "stdout.log")) + `</string>
  <key>StandardErrorPath</key>
  <string>` + esc(filepath.Join(t.DailyLogDir(), "stderr.log")) + `</string>
</dict>
</plist>
`
}

// shq is s single-quoted for sh.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// DailyScript is the wrapper: the job, then one line appended to
// daily.log (and printed): TIMESTAMP ok|failed (exit N) in Ns.
func (t *Tool) DailyScript() string {
	return `#!/bin/sh
# Generated by o-inventory daily install; o-inventory daily run runs it now.
# Per-repo CHANGELOG.md files are written, never committed; only the hub commits.
log=` + shq(t.dailyLog()) + `
mkdir -p "$(dirname "$log")"
start=$(date +%s)
` + DailyJob + `
code=$?
if [ "$code" -eq 0 ]; then result=ok; else result="failed (exit $code)"; fi
line="$(date +%Y-%m-%dT%H:%M:%S%z) $result in $(( $(date +%s) - start ))s"
echo "$line" >> "$log"
echo "$line"
exit "$code"
`
}

// DailyCommand runs daily VERB from the command line.
func (t *Tool) DailyCommand(ctx context.Context, args []string) error {
	f, err := parseFlags("daily", args, []string{"yes"}, []string{"out", "hour", "minute", "json", "target"})
	if err != nil {
		return err
	}
	usage := fmt.Errorf("usage: %s daily plist|install|uninstall|status|run [--out PATH] [--hour H --minute M]", t.Prog)
	if len(f.words) != 1 {
		return usage
	}
	if raw, ok := f.values["json"]; ok && raw != "" {
		a, err := ochazuke.ParseArguments(raw)
		if err != nil {
			return err
		}
		for k, v := range a {
			f.values[k] = fmt.Sprint(v)
		}
	}
	o := DailyOptions{Out: f.values["out"], Hour: 9}
	for name, dst := range map[string]*int{"hour": &o.Hour, "minute": &o.Minute} {
		if v := f.values[name]; v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("daily: --%s %q is not a number", name, v)
			}
			*dst = n
		}
	}
	if _, ok := f.values["json"]; !ok {
		return t.Daily(ctx, f.words[0], o, t.Stdout)
	}
	// A host's call (BIN daily VERB --json ARGS --yes): the lines as JSON
	// Lines of strings, as the catalog's daily verbs answer.
	var buf bytes.Buffer
	err = t.Daily(ctx, f.words[0], o, &buf)
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line != "" {
			data, _ := json.Marshal(line)
			fmt.Fprintf(t.Stdout, "%s\n", data)
		}
	}
	return err
}

// Daily runs one daily verb, printing its lines to w.
func (t *Tool) Daily(ctx context.Context, verb string, o DailyOptions, w io.Writer) error {
	if o.Hour < 0 || o.Hour > 23 || o.Minute < 0 || o.Minute > 59 {
		return fmt.Errorf("daily: %02d:%02d is not a time of day", o.Hour, o.Minute)
	}
	switch verb {
	case "plist":
		plist := t.DailyPlist(o.Hour, o.Minute)
		if o.Out == "" {
			_, err := io.WriteString(w, plist)
			return err
		}
		if err := writeFile(o.Out, []byte(plist)); err != nil {
			return err
		}
		fmt.Fprintf(w, "wrote %s\n", t.tilde(o.Out))
		return nil
	case "install":
		return t.dailyInstall(ctx, o, w)
	case "uninstall":
		return t.dailyUninstall(ctx, w)
	case "status":
		return t.dailyStatus(ctx, w)
	case "run":
		return t.dailyRun(ctx, w)
	}
	return fmt.Errorf("daily: unknown verb %q (plist, install, uninstall, status, run)", verb)
}

// writeIfChanged writes data at path with mode; "wrote" or "unchanged".
func writeIfChanged(path string, data []byte, mode os.FileMode) (string, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return "unchanged", os.Chmod(path, mode)
	}
	if err := writeFile(path, data); err != nil {
		return "", err
	}
	return "wrote", os.Chmod(path, mode)
}

func (t *Tool) writeScript() (string, error) {
	return writeIfChanged(t.DailyScriptPath(), []byte(t.DailyScript()), 0o755)
}

func (t *Tool) dailyInstall(ctx context.Context, o DailyOptions, w io.Writer) error {
	what, err := t.writeScript()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s %s (755)\n", what, t.tilde(t.DailyScriptPath()))
	for _, dir := range []string{t.DailyLogDir(), t.InventoryDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		fmt.Fprintf(w, "ensured %s/\n", t.tilde(dir))
	}
	plist := t.DailyPlistPath()
	what, err = writeIfChanged(plist, []byte(t.DailyPlist(o.Hour, o.Minute)), 0o644)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "%s %s (%02d:%02d daily)\n", what, t.tilde(plist), o.Hour, o.Minute)
	if out, err := t.exec(ctx, "plutil", "-lint", plist); err != nil {
		return fmt.Errorf("plutil -lint %s: %w: %s", t.tilde(plist), err, out)
	}
	fmt.Fprintf(w, "plutil -lint: OK\n")
	booted := false
	if _, err := t.exec(ctx, "launchctl", "print", t.service()); err == nil {
		if out, err := t.exec(ctx, "launchctl", "bootout", t.service()); err != nil {
			return fmt.Errorf("launchctl bootout %s: %w: %s", t.service(), err, out)
		}
		booted = true
		fmt.Fprintf(w, "launchctl bootout %s: was loaded, unloaded\n", t.service())
	}
	// Right after a bootout launchd can refuse the bootstrap for a moment
	// (error 5); try again briefly.
	var out string
	for try := 0; ; try++ {
		if out, err = t.exec(ctx, "launchctl", "bootstrap", t.domain(), plist); err == nil || !booted || try == 9 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("launchctl bootstrap %s %s: %w: %s", t.domain(), t.tilde(plist), err, out)
	}
	fmt.Fprintf(w, "launchctl bootstrap %s %s: ok\n", t.domain(), t.tilde(plist))
	out, err = t.exec(ctx, "launchctl", "print", t.service())
	if err != nil {
		return fmt.Errorf("launchctl print %s: %w: %s", t.service(), err, out)
	}
	fmt.Fprintf(w, "launchctl print %s: loaded (%s)\n", t.service(), printSummary(out))
	return nil
}

func (t *Tool) dailyUninstall(ctx context.Context, w io.Writer) error {
	if _, err := t.exec(ctx, "launchctl", "print", t.service()); err == nil {
		if out, err := t.exec(ctx, "launchctl", "bootout", t.service()); err != nil {
			return fmt.Errorf("launchctl bootout %s: %w: %s", t.service(), err, out)
		}
		fmt.Fprintf(w, "launchctl bootout %s: unloaded\n", t.service())
	} else {
		fmt.Fprintf(w, "launchctl bootout %s: not loaded\n", t.service())
	}
	for _, path := range []string{t.DailyPlistPath(), t.DailyScriptPath()} {
		switch err := os.Remove(path); {
		case err == nil:
			fmt.Fprintf(w, "removed %s\n", t.tilde(path))
		case errors.Is(err, os.ErrNotExist):
			fmt.Fprintf(w, "absent %s\n", t.tilde(path))
		default:
			return err
		}
	}
	fmt.Fprintf(w, "kept %s/\n", t.tilde(t.DailyLogDir()))
	return nil
}

var (
	plistHour   = regexp.MustCompile(`<key>Hour</key>\s*<integer>(\d+)</integer>`)
	plistMinute = regexp.MustCompile(`<key>Minute</key>\s*<integer>(\d+)</integer>`)
)

func (t *Tool) dailyStatus(ctx context.Context, w io.Writer) error {
	plist := t.DailyPlistPath()
	data, err := os.ReadFile(plist)
	if err != nil {
		fmt.Fprintf(w, "plist:    %s missing (daily install writes it)\n", t.tilde(plist))
	} else {
		fmt.Fprintf(w, "plist:    %s\n", t.tilde(plist))
	}
	if out, err := t.exec(ctx, "launchctl", "print", t.service()); err == nil {
		fmt.Fprintf(w, "launchd:  loaded as %s (%s)\n", t.service(), printSummary(out))
	} else {
		fmt.Fprintf(w, "launchd:  not loaded in %s\n", t.domain())
	}
	if last := lastLogLine(t.dailyLog()); last != "" {
		fmt.Fprintf(w, "last run: %s\n", last)
	} else {
		fmt.Fprintf(w, "last run: never (no %s)\n", t.tilde(t.dailyLog()))
	}
	if data == nil {
		fmt.Fprintf(w, "next run: none scheduled\n")
		return nil
	}
	hour, minute := 9, 0
	if m := plistHour.FindSubmatch(data); m != nil {
		hour, _ = strconv.Atoi(string(m[1]))
	}
	if m := plistMinute.FindSubmatch(data); m != nil {
		minute, _ = strconv.Atoi(string(m[1]))
	}
	fmt.Fprintf(w, "next run: %s\n", nextRun(t.now(), hour, minute).Format("2006-01-02 15:04 MST (Mon)"))
	return nil
}

// nextRun is the first HOUR:MINUTE after now, in now's zone.
func nextRun(now time.Time, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, now.Location())
	}
	return next
}

// lastLogLine is the last non-blank line of the run log, "" without one.
func lastLogLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n \t"), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// printSummary is the top-level state, runs and last exit code of a
// launchctl print answer, comma separated.
func printSummary(out string) string {
	var parts []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "\t\t") {
			continue
		}
		key, _, _ := strings.Cut(strings.TrimSpace(line), " = ")
		switch key {
		case "state", "runs", "last exit code":
			parts = append(parts, strings.TrimSpace(line))
		}
	}
	if len(parts) == 0 {
		return "no state reported"
	}
	return strings.Join(parts, ", ")
}

func (t *Tool) dailyRun(ctx context.Context, w io.Writer) error {
	if _, err := t.writeScript(); err != nil {
		return err
	}
	env := append([]string{}, t.Env...)
	for _, kv := range t.dailyEnv() {
		env = append(env, kv[0]+"="+kv[1])
	}
	cmd := exec.CommandContext(ctx, "/bin/sh", t.DailyScriptPath())
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = env, t.Home, w, t.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("daily run: %w (see %s)", err, t.tilde(t.dailyLog()))
	}
	return nil
}

// exec runs NAME (found on the tool's PATH) with args; its combined
// output, trimmed.
func (t *Tool) exec(ctx context.Context, name string, args ...string) (string, error) {
	bin, err := t.lookPath(name)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = t.Env
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// lookPath is name's first executable on the tool's PATH.
func (t *Tool) lookPath(name string) (string, error) {
	for _, dir := range filepath.SplitList(t.getenv("PATH")) {
		full := filepath.Join(dir, name)
		if info, err := os.Stat(full); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return full, nil
		}
	}
	return "", fmt.Errorf("%s not found on PATH", name)
}
