// protogen writes the Web UI's view of tend's protocol from the Go sources: internal/server/web/core/proto.js (the
// Proto, frame types, error codes, push names, the coordinator's methods, the built-in workflows, event kinds, the family table
// and the density table) and proto.schema.json (the frames, hello, state.watch and output events as JSON Schema). A test fails
// while either file differs from what this writes.
//
//	go run ./tools/protogen
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
	"github.com/oxsean/fav/internal/workflow"
)

const (
	jsPath     = "internal/server/web/core/proto.js"
	schemaPath = "internal/server/web/core/proto.schema.json"
)

func main() {
	root, err := moduleRoot()
	if err == nil {
		err = write(root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "protogen:", err)
		os.Exit(1)
	}
}

func write(root string) error {
	js, schema, err := generate(root)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, jsPath), js, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, schemaPath), schema, 0o644)
}

// moduleRoot is the directory of the go.mod at or above the working directory.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		up := filepath.Dir(dir)
		if up == dir {
			return "", errors.New("no go.mod above the working directory")
		}
		dir = up
	}
}

func generate(root string) (js, schema []byte, err error) {
	js, err = protoJS(root)
	if err != nil {
		return nil, nil, err
	}
	schema, err = protoSchema(root)
	return js, schema, err
}

// constant is one const spec with a literal value, in source order.
type constant struct {
	name, value string // value: a Go literal, a quoted string or a number
}

// constants are the exported consts of the package in dir whose names start with prefix, in the order of its files and
// their lines. Only literal values are taken: the protocol's constants are all written out.
func constants(root, dir, prefix string) ([]constant, error) {
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(root, dir, "*.go"))
	if err != nil {
		return nil, err
	}
	var out []constant
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range file.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, s := range g.Specs {
				vs := s.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if !strings.HasPrefix(n.Name, prefix) || i >= len(vs.Values) {
						continue
					}
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
						out = append(out, constant{n.Name, lit.Value})
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no constant %s* in %s", prefix, dir)
	}
	return out, nil
}

// jsName is a Go constant's name without its prefix, in lower camel case: CodeBadRequest → badRequest, FamilyMCP → mcp.
func jsName(name, prefix string) string {
	r := []rune(strings.TrimPrefix(name, prefix))
	for i := range r {
		if !unicode.IsUpper(r[i]) || i > 0 && i+1 < len(r) && unicode.IsLower(r[i+1]) {
			break
		}
		r[i] = unicode.ToLower(r[i])
	}
	return string(r)
}

// quote is a JS string literal in the Web UI's style.
func quote(s string) string {
	if strings.ContainsAny(s, "'\\\n") {
		b, _ := json.Marshal(s)
		return string(b)
	}
	return "'" + s + "'"
}

func goString(lit string) (string, error) { return strconv.Unquote(lit) }

type jsFile struct{ bytes.Buffer }

func (b *jsFile) object(doc, name string, keys []string, value func(string) string) {
	fmt.Fprintf(b, "\n// %s\nexport const %s = Object.freeze({\n", doc, name)
	for _, k := range keys {
		fmt.Fprintf(b, "  %s: %s,\n", k, value(k))
	}
	b.WriteString("});\n")
}

// consts writes the constants of dir with each prefix as one frozen object.
func (b *jsFile) consts(root, doc, name string, from ...[2]string) error {
	var keys []string
	vals := map[string]string{}
	for _, f := range from {
		cs, err := constants(root, f[0], f[1])
		if err != nil {
			return err
		}
		for _, c := range cs {
			v, err := goString(c.value)
			if err != nil {
				return fmt.Errorf("%s: %v", c.name, err)
			}
			k := jsName(c.name, f[1])
			if _, dup := vals[k]; dup {
				return fmt.Errorf("%s: %s twice", name, k)
			}
			keys = append(keys, k)
			vals[k] = quote(v)
		}
	}
	b.object(doc, name, keys, func(k string) string { return vals[k] })
	return nil
}

func protoJS(root string) ([]byte, error) {
	var b jsFile
	b.WriteString("// Code generated by go run ./tools/protogen; DO NOT EDIT.\n")
	b.WriteString("// tend's protocol as the Go sources state it (docs/design/runs/wire.md, docs/design/runs/output.md).\n")
	proto, err := constants(root, "internal/wire", "Proto")
	if err != nil {
		return nil, err
	}
	if proto[0].value != strconv.Itoa(wire.Proto) {
		return nil, fmt.Errorf("wire.Proto: read %s, built with %d", proto[0].value, wire.Proto)
	}
	fmt.Fprintf(&b, "\n// wire.Proto: both ends of a connection speak the same.\nexport const PROTO = %d;\n", wire.Proto)
	steps := []struct {
		doc, name string
		from      [][2]string
	}{
		{"wire.Type*: a frame's type.", "frame", [][2]string{{"internal/wire", "Type"}}},
		{"wire.Code*: an error's code, stable and never localized.", "code", [][2]string{{"internal/wire", "Code"}}},
		{"wire.PushOpen and coord.Push*: what a stream pushes; open comes first.", "push", [][2]string{{"internal/wire", "Push"}, {"internal/coord", "Push"}}},
		{"wire.Mode*: how a stream opens.", "mode", [][2]string{{"internal/wire", "Mode"}}},
		{"output.Kind*: a run's output events.", "kind", [][2]string{{"internal/output", "Kind"}}},
		{"output.Family*: what a tool call does.", "family", [][2]string{{"internal/output", "Family"}}},
	}
	for _, s := range steps {
		if err := b.consts(root, s.doc, s.name, s.from...); err != nil {
			return nil, err
		}
	}
	b.WriteString("\n// coord.Methods: what the coordinator answers a client, as hello lists them.\nexport const methods = Object.freeze([\n")
	for _, m := range coord.Methods {
		fmt.Fprintf(&b, "  %s,\n", quote(m))
	}
	b.WriteString("]);\n")
	b.WriteString("\n// workflow.Builtins: the workflows a task or project may name besides its project's own.\nexport const workflows = Object.freeze([")
	for i, n := range workflow.Names(nil) {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quote(n))
	}
	b.WriteString("]);\n")
	tools := slices.Sorted(func(yield func(string) bool) {
		for k := range output.Families {
			if !yield(k) {
				return
			}
		}
	})
	b.object("output.Families: a tool's family by the name claude or codex calls it; another name is other, claude's\n// mcp__server__tool is mcp.",
		"tools", tools, func(k string) string { return quote(output.Families[k]) })
	b.WriteString(densityJS())
	return b.Bytes(), nil
}

// densityJS is output's density table: the densities, the default, how each kind of step shows at each, and the line
// counts they cut at.
func densityJS() string {
	var b strings.Builder
	b.WriteString("\n// output.Densities, DensityShow and the line counts: how much of a run's output a timeline shows.\nexport const density = Object.freeze({\n  names: Object.freeze([")
	for i, d := range output.Densities {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(quote(d))
	}
	fmt.Fprintf(&b, "]),\n  default: %s,\n  show: Object.freeze({\n", quote(output.DefaultDensity))
	kinds := slices.Sorted(func(yield func(string) bool) {
		for k := range output.DensityShow {
			if !yield(k) {
				return
			}
		}
	})
	for _, k := range kinds {
		s := output.DensityShow[k]
		fmt.Fprintf(&b, "    %s: Object.freeze([%s, %s, %s]),\n", k, quote(s[0]), quote(s[1]), quote(s[2]))
	}
	b.WriteString("  }),\n")
	for _, n := range []struct {
		name string
		v    int
	}{{"briefLines", output.BriefLines}, {"sayFold", output.SayFold}, {"diffCut", output.DiffCut},
		{"failTail", output.FailTail}, {"detailEnds", output.DetailEnds}, {"runningTail", output.RunningTail}} {
		fmt.Fprintf(&b, "  %s: %d,\n", n.name, n.v)
	}
	b.WriteString("});\n")
	return b.String()
}

// schemaTypes are the types the schema defines: the frames, hello, state.watch's params and pushes, output events.
var schemaTypes = []any{
	wire.Frame{}, wire.Error{}, wire.Open{}, wire.Ended{},
	remote.HelloParams{}, remote.Hello{},
	coord.WatchParams{}, coord.Snapshot{}, coord.Live{},
	output.Event{},
}

// enums close a field's values to the constants that name them.
func enums(root string) (map[string][]string, error) {
	out := map[string][]string{}
	for field, from := range map[string][2]string{
		"wire.Frame.type":     {"internal/wire", "Type"},
		"wire.Open.mode":      {"internal/wire", "Mode"},
		"output.Event.kind":   {"internal/output", "Kind"},
		"output.Event.family": {"internal/output", "Family"},
	} {
		cs, err := constants(root, from[0], from[1])
		if err != nil {
			return nil, err
		}
		for _, c := range cs {
			v, err := goString(c.value)
			if err != nil {
				return nil, err
			}
			out[field] = append(out[field], v)
		}
	}
	return out, nil
}

var (
	rawType  = reflect.TypeFor[json.RawMessage]()
	timeType = reflect.TypeFor[time.Time]()
)

type schemaGen struct {
	defs  map[string]any
	enums map[string][]string
}

func defName(t reflect.Type) string {
	return t.PkgPath()[strings.LastIndex(t.PkgPath(), "/")+1:] + "." + t.Name()
}

func (g *schemaGen) of(t reflect.Type) any {
	switch {
	case t == rawType:
		return map[string]any{}
	case t == timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return g.of(t.Elem())
	case reflect.Interface:
		return map[string]any{}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": g.of(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": g.of(t.Elem())}
	case reflect.Struct:
		name := defName(t)
		if _, ok := g.defs[name]; !ok {
			g.defs[name] = nil
			g.defs[name] = g.object(t, name)
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	}
	return map[string]any{}
}

func (g *schemaGen) object(t reflect.Type, name string) map[string]any {
	props := map[string]any{}
	var required []string
	var fields func(t reflect.Type)
	fields = func(t reflect.Type) {
		for f := range t.Fields() {
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			key, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && key == "" && f.Type.Kind() == reflect.Struct {
				fields(f.Type)
				continue
			}
			if key == "" {
				key = f.Name
			}
			p := g.of(f.Type)
			if vs, ok := g.enums[name+"."+key]; ok {
				p = map[string]any{"type": "string", "enum": vs}
			}
			props[key] = p
			if !strings.Contains(","+opts+",", ",omitempty,") && !strings.Contains(","+opts+",", ",omitzero,") {
				required = append(required, key)
			}
		}
	}
	fields(t)
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func protoSchema(root string) ([]byte, error) {
	es, err := enums(root)
	if err != nil {
		return nil, err
	}
	g := &schemaGen{defs: map[string]any{}, enums: es}
	for _, v := range schemaTypes {
		g.of(reflect.TypeOf(v))
	}
	b, err := json.MarshalIndent(map[string]any{
		"$schema":  "https://json-schema.org/draft/2020-12/schema",
		"$comment": "Code generated by go run ./tools/protogen; DO NOT EDIT.",
		"title":    fmt.Sprintf("tend protocol %d", wire.Proto),
		"$defs":    g.defs,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
