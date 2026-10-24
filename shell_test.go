package main

import (
	"encoding/json"
	"testing"
)

func TestCatalogAnswersAndBindingsValidate(t *testing.T) {
	f := newFixture(t)
	out := f.mustRun("catalog")
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc["name"] != "o-inventory" {
		t.Fatalf("catalog %v: %s", err, out)
	}
	if _, err := f.tool0().shell().Runtime.ValidateBindings(); err != nil {
		t.Fatal(err)
	}
}
