package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var keyRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)

// every i18n.T key exists in en and zh, both files have the same keys and placeholders, no unused keys
func TestEveryKeyTranslated(t *testing.T) {
	used, byT := map[string]string{}, map[string]string{}
	literals := map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir("../..", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					literals[s] = true
				}
			}
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "T" && sel.Sel.Name != "F" && sel.Sel.Name != "E") {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "i18n" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if !keyRe.MatchString(s) {
				t.Errorf("%s: i18n.T(%q) is not a semantic key", fset.Position(lit.Pos()), s)
			}
			used[s] = fset.Position(lit.Pos()).String()
			if sel.Sel.Name == "T" {
				byT[s] = used[s]
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	en, zh := tables[EN], tables[ZH]
	for k, where := range byT {
		if plural.MatchString(en[k]) {
			t.Errorf("%s: %q has {one|other}, which only F and E fill", where, k)
		}
	}
	for k, where := range used {
		if _, ok := en[k]; !ok {
			t.Errorf("missing en: %q  (%s)", k, where)
		}
		if _, ok := zh[k]; !ok {
			t.Errorf("missing zh: %q  (%s)", k, where)
		}
	}
	verbs := regexp.MustCompile(`%[-+# 0-9.]*[a-zA-Z%]`)
	for k, e := range en {
		z, ok := zh[k]
		if !ok {
			t.Errorf("zh lacks %q", k)
			continue
		}
		if a, b := verbs.FindAllString(e, -1), verbs.FindAllString(z, -1); strings.Join(a, "") != strings.Join(b, "") {
			t.Errorf("placeholders differ for %q: en %v zh %v", k, a, b)
		}
		if !literals[k] {
			t.Errorf("orphan key (nothing uses it): %q", k)
		}
	}
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("en lacks %q", k)
		}
	}
}

// ⚠️ a heuristic: a word after %d ending in s is taken for a plural noun, a few verbs for a count's verb
var (
	notPlural  = map[string]bool{"is": true, "was": true, "has": true, "its": true, "this": true, "us": true, "plus": true, "ms": true, "as": true, "less": true, "does": true}
	countVerbs = map[string]bool{"need": true, "have": true, "are": true, "were": true}
	countStop  = regexp.MustCompile(`%|[,;:·()\[\]/.!?\n—–…+×]`)
)

// disagreeing is the first English word after a %d that has one form for every count, or "".
func disagreeing(s string) string {
	s = plural.ReplaceAllString(s, "N")
	for _, at := range regexp.MustCompile(`%d`).FindAllStringIndex(s, -1) {
		rest := s[at[1]:]
		if strings.HasPrefix(rest, "%") {
			continue
		}
		if i := countStop.FindStringIndex(rest); i != nil {
			rest = rest[:i[0]]
		}
		words := strings.Fields(rest)
		if len(words) > 3 {
			words = words[:3]
		}
		for i, w := range words {
			w = strings.ToLower(strings.Trim(w, `"'`))
			if i == 0 && countVerbs[w] || len(w) > 2 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !notPlural[w] {
				return w
			}
		}
	}
	return ""
}

func TestEnglishCountsAgree(t *testing.T) {
	for k, e := range tables[EN] {
		if w := disagreeing(e); w != "" {
			t.Errorf("%q: %q after %%d reads wrong for 1: write {one|other}", k, w)
		}
		for _, at := range plural.FindAllStringIndex(e, -1) {
			if !strings.Contains(e[:at[0]], "%d") {
				t.Errorf("%q: {one|other} with no %%d before it", k)
			}
		}
	}
}
