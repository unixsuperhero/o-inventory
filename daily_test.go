package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// goldenCheck compares got with testdata/NAME (UPDATE_GOLDEN=1 rewrites it).
func goldenCheck(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs:\n%s", name, got)
	}
}

func TestDailyPlistAndScriptGolden(t *testing.T) {
	var out bytes.Buffer
	tool := &Tool{Stdout: &out, Stderr: &out, Home: "/Users/someone", UID: 4242, Prog: "o-inventory", Env: []string{"PATH=/usr/bin:/bin"}}
	if err := tool.DailyCommand(context.Background(), []string{"plist"}); err != nil {
		t.Fatal(err)
	}
	goldenCheck(t, "daily.plist.golden", out.String())
	goldenCheck(t, "daily.sh.golden", tool.DailyScript())
	out.Reset()
	if err := tool.DailyCommand(context.Background(), []string{"plist", "--hour", "7", "--minute=30"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "<key>Hour</key>\n    <integer>7</integer>\n    <key>Minute</key>\n    <integer>30</integer>") {
		t.Fatalf("hour/minute:\n%s", out.String())
	}
	if err := tool.DailyCommand(context.Background(), []string{"plist", "--hour", "24"}); err == nil {
		t.Fatal("hour 24 accepted")
	}
	// --out writes the file instead.
	dir := t.TempDir()
	out.Reset()
	if err := tool.DailyCommand(context.Background(), []string{"plist", "--out", filepath.Join(dir, "x.plist")}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "x.plist")); !strings.Contains(string(data), DailyLabel) || !strings.HasPrefix(out.String(), "wrote ") {
		t.Fatalf("--out: %q\n%s", out.String(), data)
	}
}

// fakeLaunchd puts fake launchctl and plutil on the fixture's PATH: each
// records its argv in data/argv; launchctl keeps "loaded" as a file.
func fakeLaunchd(f *fixture) string {
	argv := filepath.Join(f.data, "argv")
	loaded := filepath.Join(f.data, "loaded")
	f.write(filepath.Join(f.bin, "launchctl"), `#!/bin/sh
echo "launchctl $*" >> '`+argv+`'
case "$1" in
  print) [ -f '`+loaded+`' ] || { echo "Could not find service" >&2; exit 113; }
         printf '%s = {\n\tactive count = 0\n\tstate = not running\n\truns = 0\n\tlast exit code = (never exited)\n\tendpoints = {\n\t\tstate = nested\n\t}\n}\n' "$2" ;;
  bootstrap) /usr/bin/touch '`+loaded+`' ;;
  bootout) /bin/rm -f '`+loaded+`' ;;
esac
`)
	f.write(filepath.Join(f.bin, "plutil"), "#!/bin/sh\necho \"plutil $*\" >> '"+argv+"'\necho \"$2: OK\"\n")
	os.Chmod(filepath.Join(f.bin, "launchctl"), 0o755)
	os.Chmod(filepath.Join(f.bin, "plutil"), 0o755)
	return argv
}

func (f *fixture) daily(args ...string) string {
	f.t.Helper()
	f.out.Reset()
	tool := f.tool0()
	tool.UID = 4242
	if err := tool.DailyCommand(context.Background(), args); err != nil {
		f.t.Fatalf("daily %v: %v\n%s", args, err, f.out.String())
	}
	return f.out.String()
}

func TestDailyInstallUninstallAgainstFakeLaunchctl(t *testing.T) {
	f := newFixture(t)
	argv := fakeLaunchd(f)
	svc := "gui/4242/" + DailyLabel
	plist := filepath.Join(f.home, "Library", "LaunchAgents", DailyLabel+".plist")
	want := `wrote ~/.local/share/o-inventory/daily.sh (755)
ensured ~/Library/Logs/o-inventory/
ensured ~/hub/inventory/
wrote ~/Library/LaunchAgents/` + DailyLabel + `.plist (09:00 daily)
plutil -lint: OK
launchctl bootstrap gui/4242 ~/Library/LaunchAgents/` + DailyLabel + `.plist: ok
launchctl print ` + svc + `: loaded (state = not running, runs = 0, last exit code = (never exited))
`
	if got := f.daily("install"); got != want {
		t.Fatalf("install:\n%s", got)
	}
	if info, err := os.Stat(filepath.Join(f.home, ".local", "share", "o-inventory", "daily.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("wrapper %v %v", info, err)
	}
	for _, d := range []string{"Library/Logs/o-inventory", "hub/inventory"} {
		if info, err := os.Stat(filepath.Join(f.home, d)); err != nil || !info.IsDir() {
			t.Fatalf("%s: %v", d, err)
		}
	}
	// Again: unchanged files, bootout first, then bootstrap.
	again := f.daily("install")
	for _, line := range []string{"unchanged ~/.local/share/o-inventory/daily.sh (755)", "unchanged ~/Library/LaunchAgents/" + DailyLabel + ".plist (09:00 daily)", "launchctl bootout " + svc + ": was loaded, unloaded"} {
		if !strings.Contains(again, line+"\n") {
			t.Fatalf("second install lacks %q:\n%s", line, again)
		}
	}
	f.daily("uninstall")
	calls, _ := os.ReadFile(argv)
	wantCalls := `plutil -lint ` + plist + `
launchctl print ` + svc + `
launchctl bootstrap gui/4242 ` + plist + `
launchctl print ` + svc + `
plutil -lint ` + plist + `
launchctl print ` + svc + `
launchctl bootout ` + svc + `
launchctl bootstrap gui/4242 ` + plist + `
launchctl print ` + svc + `
launchctl print ` + svc + `
launchctl bootout ` + svc + `
`
	if string(calls) != wantCalls {
		t.Fatalf("argv:\n%s", calls)
	}
	for _, p := range []string{plist, filepath.Join(f.home, ".local", "share", "o-inventory", "daily.sh")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s survived uninstall: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(f.home, "Library", "Logs", "o-inventory")); err != nil {
		t.Fatal("uninstall removed the logs")
	}
	if got := f.daily("uninstall"); got != "launchctl bootout "+svc+": not loaded\nabsent ~/Library/LaunchAgents/"+DailyLabel+".plist\nabsent ~/.local/share/o-inventory/daily.sh\nkept ~/Library/Logs/o-inventory/\n" {
		t.Fatalf("second uninstall:\n%s", got)
	}
}

func TestDailyStatusReadsTheLogAndSchedule(t *testing.T) {
	f := newFixture(t)
	fakeLaunchd(f)
	f.clock = time.Date(2026, 10, 24, 20, 10, 0, 0, time.UTC)
	if got := f.daily("status"); got != "plist:    ~/Library/LaunchAgents/"+DailyLabel+".plist missing (daily install writes it)\nlaunchd:  not loaded in gui/4242\nlast run: never (no ~/Library/Logs/o-inventory/daily.log)\nnext run: none scheduled\n" {
		t.Fatalf("before install:\n%s", got)
	}
	f.daily("install", "--hour", "21", "--minute", "5")
	f.write(filepath.Join(f.home, "Library", "Logs", "o-inventory", "daily.log"), "2026-10-23T21:05:02+0000 ok in 3s\n2026-10-24T21:05:01+0000 failed (exit 1) in 2s\n\n")
	want := "plist:    ~/Library/LaunchAgents/" + DailyLabel + ".plist\nlaunchd:  loaded as gui/4242/" + DailyLabel + " (state = not running, runs = 0, last exit code = (never exited))\nlast run: 2026-10-24T21:05:01+0000 failed (exit 1) in 2s\nnext run: 2026-10-24 21:05 UTC (Sat)\n"
	if got := f.daily("status"); got != want {
		t.Fatalf("status:\n%s", got)
	}
	f.clock = time.Date(2026, 10, 24, 21, 5, 0, 0, time.UTC)
	if got := f.daily("status"); !strings.HasSuffix(got, "next run: 2026-10-25 21:05 UTC (Sun)\n") {
		t.Fatalf("at the minute:\n%s", got)
	}
}

func TestDailyRunRunsTheWrapperAndLogs(t *testing.T) {
	f := newFixture(t)
	calls := filepath.Join(f.data, "calls")
	f.write(filepath.Join(f.home, "bin", "o-inventory"), "#!/bin/sh\necho \"$* MTM_SERVER=$MTM_SERVER\" >> '"+calls+"'\n")
	os.Chmod(filepath.Join(f.home, "bin", "o-inventory"), 0o755)
	out := f.daily("run")
	got, _ := os.ReadFile(calls)
	if string(got) != "snapshot --commit --quiet MTM_SERVER=default\nchangelog --write-repos --commit --quiet MTM_SERVER=default\n" {
		t.Fatalf("calls:\n%s", got)
	}
	logged := lastLogLine(filepath.Join(f.home, "Library", "Logs", "o-inventory", "daily.log"))
	if !strings.Contains(logged, " ok in ") || !strings.Contains(out, logged) {
		t.Fatalf("log %q out %q", logged, out)
	}
}

func TestDailyVerbsAreInTheCatalog(t *testing.T) {
	f := newFixture(t)
	out := f.mustRun("catalog")
	for _, v := range dailyVerbs {
		if !strings.Contains(out, `"name": "`+v.Name+`"`) {
			t.Fatalf("catalog lacks daily %s", v.Name)
		}
	}
	if !strings.Contains(out, `"name": "daily"`) {
		t.Fatal("catalog lacks the daily noun")
	}
}
