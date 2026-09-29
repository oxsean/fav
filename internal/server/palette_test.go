package server

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The page's action table: every action has one key of its own and a Chinese and an English name, and searching by
// either name finds it first.
func TestEveryActionHasAKeyAndIsFoundByEitherName(t *testing.T) {
	nodeBin := nodeJS(t)
	palette, _ := filepath.Abs(filepath.Join("web", "palette.js"))
	script := `const fs=require('fs'),vm=require('vm');globalThis.esc=s=>String(s);
vm.runInThisContext(fs.readFileSync(process.argv[1],'utf8'));
const out=Palette.actions.map(a=>{const w=paletteWords['act.'+a.id]||[];return {id:a.id,key:a.key,zh:w[0]||'',en:w[1]||'',
  byZh:Palette.search(w[0]||'?')[0]?.id,byEn:Palette.search(w[1]||'?')[0]?.id,
  byKey:Palette.keyName(a.key==='Mod+K'?{key:'k',metaKey:true}:a.key.startsWith('Shift+')?{key:a.key.slice(6),shiftKey:true}:{key:a.key.split(' ').pop()})}});
out.push({ime:[Palette.keyName({key:'？'}),Palette.keyName({key:'、'})]});
process.stdout.write(JSON.stringify(out));`
	b, err := exec.Command(nodeBin, "-e", script, palette).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var out []struct {
		ID, Key, Zh, En, ByZh, ByEn, ByKey string
		IME                                []string
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if ime := out[len(out)-1].IME; len(ime) != 2 || ime[0] != "?" || ime[1] != "/" {
		t.Fatalf("an input method's marks are the keys they stand for: %v", ime)
	}
	keys := map[string]string{}
	for _, a := range out[:len(out)-1] {
		if a.Key == "" || a.Zh == "" || a.En == "" {
			t.Errorf("%s: key %q, names %q / %q", a.ID, a.Key, a.Zh, a.En)
		}
		if other, ok := keys[a.Key]; ok {
			t.Errorf("%s and %s share %s", a.ID, other, a.Key)
		}
		keys[a.Key] = a.ID
		if a.ByZh != a.ID || a.ByEn != a.ID {
			t.Errorf("%s: its names find %q and %q", a.ID, a.ByZh, a.ByEn)
		}
		if last := a.Key[strings.LastIndex(a.Key, " ")+1:]; a.ByKey != last {
			t.Errorf("%s: a press of %s reads as %q", a.ID, a.Key, a.ByKey)
		}
	}
	if len(keys) < 15 {
		t.Fatalf("%d actions", len(keys))
	}
}
