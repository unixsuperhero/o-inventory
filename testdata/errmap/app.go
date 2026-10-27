package fix

import (
	"errors"
	"fmt"
	"os"

	"example.com/fix/rt"
	"example.com/fix/store"
)

const taskNoun = "task"

type App struct{ r *rt.Runtime }

func (a *App) define() {
	a.r.Define("pane").Verb("rename", a.renamePane).Quiet().
		Verb("close", func(c rt.Ctx) rt.Result {
			return rt.Failed(errors.New("no pane is attached"))
		})
	b := a.r.Define(taskNoun)
	b.Verb("done", a.doneTask)
	a.r.Register(rt.Operation{Noun: "app", Verb: "quit", Run: func() (any, error) {
		return nil, errors.New("unreachable: quit twice")
	}})
	if a.r == nil {
		panic("invariant: no runtime")
	}
}

func (a *App) renamePane(c rt.Ctx) rt.Result {
	if c.Name == "" {
		return rt.Failed(fmt.Errorf("missing name"))
	}
	if err := a.save(c.Name); err != nil {
		c.Status("could not save " + c.Name)
		return rt.Failed(err)
	}
	c.Status("saved")
	return rt.Result{}
}

func (a *App) save(name string) error {
	if _, err := os.Stat(name); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

func (a *App) doneTask(c rt.Ctx) rt.Result {
	err := store.Load(c.Name)
	if errors.Is(err, store.ErrGone) {
		return rt.Failed(fmt.Errorf("done: %w", store.ErrGone))
	}
	switch c.Name {
	case "x":
		return rt.Result{}
	default:
		return rt.Failed(fmt.Errorf("mode %q", c.Name))
	}
}

func (a *App) defineMore() {
	add := func(verb string, fn func() (any, error)) {
		a.r.Register(rt.Operation{Noun: "macro", Verb: verb, Run: fn})
	}
	add("stop", func() (any, error) { return nil, errors.New("no macro recording") })
	for _, spec := range []struct {
		verb string
		fn   func() (any, error)
	}{
		{"undo", func() (any, error) { return nil, errors.New("nothing to undo") }},
		{verb: "redo", fn: func() (any, error) { return nil, nil }},
	} {
		fn := spec.fn
		a.r.Register(rt.Operation{Noun: "input", Verb: spec.verb, Run: func() (any, error) {
			if a.r == nil {
				return nil, errors.New("no focused input")
			}
			return fn()
		}})
	}
}
