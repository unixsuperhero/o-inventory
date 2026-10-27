package store

import "errors"

// ErrGone is returned when the pane went away.
var ErrGone = errors.New("pane is gone")

func Load(id string) error {
	if id == "" {
		return ErrGone
	}
	return nil
}
