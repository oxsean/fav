package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// proto.js and proto.schema.json are what the Go sources say now.
func TestTheGeneratedFilesAreCurrent(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	js, schema, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string][]byte{jsPath: js, schemaPath: schema} {
		got, err := os.ReadFile(filepath.Join(root, path))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is stale (%v): go run ./tools/protogen", path, err)
		}
	}
}
