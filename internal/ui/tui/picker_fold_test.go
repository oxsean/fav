package tui

import "testing"

func TestPickerFoldsSingletons(t *testing.T) {
	var items []item
	for i := range 4 {
		items = append(items, item{name: "common" + string(rune('a'+i)), count: 2})
	}
	for i := range 30 {
		items = append(items, item{name: "rare" + string(rune('a'+i)), count: 1})
	}
	o := overlay{items: items, checked: map[string]bool{"rarez": true}}
	vis := o.visible()
	if len(vis) != pickerFill {
		t.Fatalf("默认应补到 %d 行，实际 %d", pickerFill, len(vis))
	}
	if vis[4].name != "rarez" {
		t.Errorf("勾选中的单次项应紧跟常用项，实际 %s", vis[4].name)
	}
	o.filter.SetValue("rare")
	if n := len(o.visible()); n != 30 {
		t.Errorf("输入后应全量匹配 30 项，实际 %d", n)
	}
}
