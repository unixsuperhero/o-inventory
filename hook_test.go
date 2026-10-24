package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookInstallIsIdempotentAndAppends(t *testing.T) {
	f := newFixture(t)
	alpha := filepath.Join(f.home, "proj", "alpha")
	gitRepo(t, alpha, map[string]string{"feat: one": "2026-10-24T19:00:00Z"})
	hook := filepath.Join(alpha, ".git", "hooks", "post-commit")
	os.WriteFile(hook, []byte("#!/bin/sh\necho mine"), 0o755)
	f.mustRun("repos")
	if out := f.mustRun("hook", "install", "alpha"); out != "alpha: appended ~/proj/alpha/.git/hooks/post-commit\n" {
		t.Fatalf("first install %q", out)
	}
	first, _ := os.ReadFile(hook)
	if !strings.HasPrefix(string(first), "#!/bin/sh\necho mine\n"+hookBegin) || strings.Count(string(first), hookBegin) != 1 {
		t.Fatalf("hook:\n%s", first)
	}
	if n := strings.Count(hookBlock, "\n"); n != 5 { // six lines with the #!/bin/sh of a new hook
		t.Fatalf("block has %d lines", n)
	}
	if out := f.mustRun("hook", "install", alpha); out != "alpha: unchanged ~/proj/alpha/.git/hooks/post-commit\n" {
		t.Fatalf("second install %q", out)
	}
	if again, _ := os.ReadFile(hook); string(again) != string(first) {
		t.Fatal("second install changed the hook")
	}
	// An older block is replaced in place.
	os.WriteFile(hook, []byte("#!/bin/sh\n"+hookBegin+"\nold\n"+hookEnd+"\necho after\n"), 0o755)
	f.mustRun("hook", "install", "alpha")
	if got, _ := os.ReadFile(hook); string(got) != "#!/bin/sh\n"+hookBlock+"echo after\n" {
		t.Fatalf("updated hook:\n%s", got)
	}
	// --all covers every repo in repos.json; the missing ones fail.
	if _, err := f.run("hook", "install", "--all"); err == nil || !strings.Contains(err.Error(), "matomeru, ochazuke, oden") {
		t.Fatalf("--all err %v", err)
	}
	if !strings.Contains(f.out.String(), "alpha: unchanged") {
		t.Fatalf("--all out %q", f.out.String())
	}
	if out, err := f.run("check", "--jsonl"); err == nil || strings.Contains(out, "no-hook") {
		t.Fatalf("check after install: %v\n%s", err, out)
	}
}

func TestHookSnapshotsOnlyForCatalogFiles(t *testing.T) {
	f := newFixture(t)
	repo := filepath.Join(f.home, "proj", "alpha")
	gitRepo(t, repo, map[string]string{"feat: one": "2026-10-24T19:00:00Z"})
	if what, _, err := InstallHook(repo); err != nil || what != "appended" && what != "created" {
		t.Fatalf("install %q %v", what, err)
	}
	marker := filepath.Join(f.home, "snapshotted")
	fake := filepath.Join(f.home, "fakebin")
	os.MkdirAll(fake, 0o755)
	os.WriteFile(filepath.Join(fake, "o-inventory"), []byte("#!/bin/sh\necho \"$@\" >> '"+marker+"'\n"), 0o755)
	commit := func(file string) {
		os.WriteFile(filepath.Join(repo, file), []byte(file), 0o644)
		for _, args := range [][]string{{"add", file}, {"commit", "-q", "-m", "x " + file}} {
			cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
			cmd.Env = append(gitEnv(), "PATH="+fake+":"+os.Getenv("PATH"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
	}
	wait := func() string {
		for i := 0; i < 50; i++ {
			if data, err := os.ReadFile(marker); err == nil {
				return string(data)
			}
			time.Sleep(100 * time.Millisecond)
		}
		return ""
	}
	commit("README.md")
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a README-only commit snapshotted")
	}
	commit("main.go")
	if got := wait(); got != "snapshot --commit --quiet\n" {
		t.Fatalf("hook ran %q", got)
	}
}

func TestDailyInstallWritesThePlistAndNeverLoadsIt(t *testing.T) {
	f := newFixture(t)
	tool := f.tool0()
	tool.Env = append(tool.Env, "USER=someone")
	f.out.Reset()
	if err := tool.DailyCommand([]string{"install"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.home, "Library", "LaunchAgents", "com.someone.o-inventory.plist")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<string>com.someone.o-inventory</string>", "o-inventory snapshot --commit --quiet &amp;&amp; o-inventory changelog --write-repos --commit --quiet", "<key>Hour</key>"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("plist lacks %q:\n%s", want, data)
		}
	}
	if out := f.out.String(); !strings.Contains(out, "launchctl load "+path) || !strings.Contains(out, string(data)) {
		t.Fatalf("output:\n%s", out)
	}
}
