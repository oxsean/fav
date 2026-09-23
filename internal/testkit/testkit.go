// Package testkit holds helpers tests share for cross-platform fixtures.
package testkit

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"
)

// JSONString is s as a JSON string literal, quotes included, as the CLIs write it (& < > not escaped): use it to put
// native paths (Windows backslashes) into hand-written transcript lines.
func JSONString(s string) string {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(s); err != nil {
		panic(err)
	}
	return string(bytes.TrimRight(b.Bytes(), "\n"))
}

// PosixOnly skips a test whose fixtures spell POSIX paths; internal/fixture covers the same behaviour with native paths.
func PosixOnly(t testing.TB) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-path fixture; internal/fixture covers Windows")
	}
}
