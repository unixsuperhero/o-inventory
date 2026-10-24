package main

import (
	"fmt"
	"html"
	"path/filepath"
	"strings"
)

// DailyCommand is the daily job's daily install: it writes the launchd
// agent ~/Library/LaunchAgents/com.USER.o-inventory.plist (09:00 local:
// snapshot --commit, then changelog --write-repos --commit) and prints it
// with the launchctl command that loads it. It never loads it.
func (t *Tool) DailyCommand(args []string) error {
	f, err := parseFlags("daily", args, nil, nil)
	if err != nil {
		return err
	}
	if len(f.words) != 1 || f.words[0] != "install" {
		return fmt.Errorf("usage: %s daily install", t.Prog)
	}
	user := t.getenv("USER")
	if user == "" {
		user = filepath.Base(t.Home)
	}
	label := "com." + user + ".o-inventory"
	path := filepath.Join(t.Home, "Library", "LaunchAgents", label+".plist")
	plist := t.plist(label)
	if err := writeFile(path, []byte(plist)); err != nil {
		return err
	}
	fmt.Fprintf(t.Stdout, "wrote %s:\n\n%s\nload it with:\n\n  launchctl load %s\n", t.tilde(path), plist, path)
	return nil
}

// DailyScript is what the daily job runs.
const DailyScript = "o-inventory snapshot --commit --quiet && o-inventory changelog --write-repos --commit --quiet"

func (t *Tool) plist(label string) string {
	esc := html.EscapeString
	logs := filepath.Join(t.Home, "Library", "Logs", "o-inventory.log")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + esc(label) + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>` + esc(DailyScript) + `</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>` + esc(t.getenv("PATH")) + `</string>
    <key>HOME</key>
    <string>` + esc(t.Home) + `</string>
    <key>MTM_SERVER</key>
    <string>default</string>
  </dict>
  <key>StartCalendarInterval</key>
  <dict>
    <key>Hour</key>
    <integer>9</integer>
    <key>Minute</key>
    <integer>0</integer>
  </dict>
  <key>StandardOutPath</key>
  <string>` + esc(logs) + `</string>
  <key>StandardErrorPath</key>
  <string>` + esc(logs) + `</string>
</dict>
</plist>
`)
	return b.String()
}
