package i18n

import "testing"

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
