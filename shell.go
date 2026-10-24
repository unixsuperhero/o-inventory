package main

import (
	"github.com/unixsuperhero/ochazuke"
	ocli "github.com/unixsuperhero/ochazuke/cli"
)

// Version is the catalog's version.
const Version = "0.1.0"

// shellNouns are the runtime's own nouns, mounted as commands.
var shellNouns = []string{}

func (t *Tool) shell() *ocli.Shell {
	r := ochazuke.NewRuntime()
	sh := ocli.New(r, "o-inventory", t.Stdin, t.Stdout, t.Stderr)
	sh.Usage = ocli.DefaultUsage(t.Prog)
	sh.Version = Version
	return sh
}
