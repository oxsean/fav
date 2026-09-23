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
	used := map[string]string{}
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
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	en, zh := tables[EN], tables[ZH]
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
