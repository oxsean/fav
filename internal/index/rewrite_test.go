package index

import (
	"bytes"
	"strings"
	"testing"
)

// Every cwd value goes through the mapping decoded, file:// kept, and comes back encoded; other fields, message bodies
// and lines the mapping leaves alone keep their bytes, and the line count stays.
func TestRewriteCwdMapsEachValueAndNothingElse(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"user","cwd":"C:\\Users\\me\\dev\\web app","message":{"content":"cd C:\\Users\\me\\dev\\web app"}}`,
		`{"type":"x","cwd":"file:///home/me/dev/webapp","cwd2":"/home/me/dev/webapp"}`,
		`{"type":"y","payload":{"cwd":"/home/me/dev/webapp/src"},"cwd":"/elsewhere"}`,
		`{"type":"z","text":"\"cwd\":\"/home/me/dev/webapp\""}`,
		`{"type":"q","cwd":"/home/me/dev/\"quoted\" \u4e2d"}`,
		`{"type":"last","cwd":"/home/me/dev/webapp"}`,
	}, "\n")
	maps := map[string]string{
		`C:\Users\me\dev\web app`:   "/Users/me/dev/web app",
		"/home/me/dev/webapp":       `D:\src\webapp`,
		"/home/me/dev/webapp/src":   `D:\src\webapp\src`,
		"/home/me/dev/\"quoted\" 中": "/x/\"quoted\" 中",
	}
	var asked []string
	var out bytes.Buffer
	lines, err := RewriteCwd(strings.NewReader(in), &out, func(cwd string) (string, bool) {
		asked = append(asked, cwd)
		to, ok := maps[cwd]
		return to, ok
	})
	if err != nil || lines != 6 {
		t.Fatalf("lines %d, %v", lines, err)
	}
	want := strings.Join([]string{
		`{"type":"user","cwd":"/Users/me/dev/web app","message":{"content":"cd C:\\Users\\me\\dev\\web app"}}`,
		`{"type":"x","cwd":"file://D:\\src\\webapp","cwd2":"/home/me/dev/webapp"}`,
		`{"type":"y","payload":{"cwd":"D:\\src\\webapp\\src"},"cwd":"/elsewhere"}`,
		`{"type":"z","text":"\"cwd\":\"/home/me/dev/webapp\""}`,
		`{"type":"q","cwd":"/x/\"quoted\" 中"}`,
		`{"type":"last","cwd":"D:\\src\\webapp"}`,
	}, "\n")
	if out.String() != want {
		t.Errorf("rewritten:\n%s\nwant:\n%s", out.String(), want)
	}
	if strings.Join(asked, "|") != strings.Join([]string{`C:\Users\me\dev\web app`, "/home/me/dev/webapp", "/home/me/dev/webapp/src",
		"/elsewhere", "/home/me/dev/\"quoted\" 中", "/home/me/dev/webapp"}, "|") {
		t.Errorf("asked about %q", asked)
	}
}

// A line the mapping does not touch, a torn value and an empty input come back byte for byte.
func TestRewriteCwdKeepsWhatItCannotRead(t *testing.T) {
	for _, in := range []string{"", "\n\n", `{"cwd":"/a` + "\n", `{"cwd":"\x"}` + "\n", `{"cwd":"/a"}` + "\n" + `{"cwd":"/b"}`} {
		var out bytes.Buffer
		lines, err := RewriteCwd(strings.NewReader(in), &out, func(string) (string, bool) { return "", false })
		if err != nil || out.String() != in || lines != strings.Count(in, "\n")+min(1, len(in)-strings.LastIndex(in, "\n")-1) {
			t.Errorf("%q: %q, %d lines, %v", in, out.String(), lines, err)
		}
	}
}
