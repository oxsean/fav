package testkit

import "testing"

func TestJSONString(t *testing.T) {
	if got, want := JSONString(`C:\a & b`), `"C:\\a & b"`; got != want {
		t.Fatalf("JSONString = %q, want %q", got, want)
	}
}
