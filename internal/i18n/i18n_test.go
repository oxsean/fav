package i18n

import (
	"fmt"
	"testing"
)

func TestTranslateAndDetect(t *testing.T) {
	defer Set(ZH)
	Set(ZH)
	if T("status.archived") != "已归档" || T("no.such.key") != "no.such.key" {
		t.Fatal("中文查表，没有的原样返回 key")
	}
	Set(EN)
	if T("status.archived") != "archived" {
		t.Fatal("英文表")
	}
	Set("")
	if T("status.archived") != "已归档" {
		t.Fatal("空值当中文")
	}
	for _, c := range []struct{ lang, want string }{{"zh_CN.UTF-8", ZH}, {"en_US.UTF-8", EN}, {"C", detectPlatform()}, {"", detectPlatform()}} {
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", c.lang)
		if got := Detect(); got != c.want {
			t.Errorf("LANG=%q → %s, want %s", c.lang, got, c.want)
		}
	}
	if Resolve(EN) != EN || Resolve(ZH) != ZH {
		t.Fatal("显式设置优先")
	}
}

func TestCountsPickTheirForm(t *testing.T) {
	for _, c := range []struct {
		s    string
		a    []any
		want string
	}{
		{"%d {file|files}", []any{1}, "1 file"},
		{"%d {file|files}", []any{0}, "0 files"},
		{"%d {file|files}", []any{int64(2)}, "2 files"},
		{"%s: %d {task|tasks} {needs|need} you, %d {run|runs}", []any{"a", 1, 3}, "a: 1 task needs you, 3 runs"},
		{"%d/%d {hit|hits}, %d%%", []any{2, 1, 50}, "2/1 hit, 50%"},
		{"%.1f MB in %d {run|runs}", []any{1.0, uint(1)}, "1.0 MB in 1 run"},
		{"%d 个文件", []any{1}, "1 个文件"},
	} {
		if got := fmt.Sprintf(counted(c.s, c.a), c.a...); got != c.want {
			t.Errorf("%q %v → %q, want %q", c.s, c.a, got, c.want)
		}
	}
}
