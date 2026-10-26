// Package archtest enforces the hexagonal dependency rules on every bounded
// context under internal/. It parses imports, so it needs no build and runs
// with the regular test suite.
//
// Layout of a context:  internal/<context>/{domain,application,ports,adapters}
//
//   - domain       imports only the standard library (and its own context's domain).
//   - application  imports domain and ports, never adapters.
//   - ports        imports domain, never adapters or application.
//   - no context imports another context's domain, application or adapters;
//     contexts talk through events or through a port.
package archtest

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const module = "github.com/CodyLionVivo/duradent/services/api"

// shared are packages any layer may import: they hold no business rules and
// no infrastructure (ids, money, clock).
var shared = []string{module + "/internal/kernel"}

type file struct {
	path    string // relative to internal/
	context string
	layer   string
	imports []string
}

func TestHexagonalDependencyRules(t *testing.T) {
	files := parseInternal(t)
	for _, f := range files {
		for _, imp := range f.imports {
			if msg := violation(f, imp); msg != "" {
				t.Errorf("%s imports %s: %s", f.path, imp, msg)
			}
		}
	}
}

func violation(f file, imp string) string {
	if f.layer == "" { // platform, kernel, archtest...
		return ""
	}
	for _, s := range shared {
		if imp == s || strings.HasPrefix(imp, s+"/") {
			return ""
		}
	}
	stdlib := !strings.Contains(strings.SplitN(imp, "/", 2)[0], ".")
	ctx, layer := classify(imp)

	switch f.layer {
	case "domain":
		if stdlib || (ctx == f.context && layer == "domain") {
			return ""
		}
		return "domain may only import the standard library"
	case "application", "ports":
		if layer == "adapters" {
			return f.layer + " must not depend on adapters"
		}
		if strings.HasPrefix(imp, module+"/internal/platform") {
			return f.layer + " must not depend on platform infrastructure; declare a port"
		}
		if f.layer == "ports" && layer == "application" {
			return "ports must not depend on application"
		}
	}
	if ctx != "" && ctx != f.context && layer != "ports" {
		return "contexts must not reach into another context's " + layer + "; use its ports or events"
	}
	return ""
}

// classify returns the context and layer of an import inside this module.
func classify(imp string) (context, layer string) {
	rest, ok := strings.CutPrefix(imp, module+"/internal/")
	if !ok {
		return "", ""
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || !isLayer(parts[1]) {
		return "", ""
	}
	return parts[0], parts[1]
}

func isLayer(s string) bool {
	switch s {
	case "domain", "application", "ports", "adapters":
		return true
	}
	return false
}

func parseInternal(t *testing.T) []file {
	t.Helper()
	root := ".." // internal/
	var out []file
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		f := file{path: filepath.ToSlash(rel)}
		if len(parts) >= 3 && isLayer(parts[1]) {
			f.context, f.layer = parts[0], parts[1]
		}
		ast, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, s := range ast.Imports {
			v, _ := strconv.Unquote(s.Path.Value)
			f.imports = append(f.imports, v)
		}
		out = append(out, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestViolationRules(t *testing.T) {
	m := module + "/internal/"
	cases := []struct {
		name    string
		f       file
		imp     string
		allowed bool
	}{
		{"domain stdlib", file{context: "catalog", layer: "domain"}, "time", true},
		{"domain third party", file{context: "catalog", layer: "domain"}, "github.com/google/uuid", false},
		{"domain kernel", file{context: "catalog", layer: "domain"}, m + "kernel/money", true},
		{"application own domain", file{context: "orders", layer: "application"}, m + "orders/domain", true},
		{"application adapters", file{context: "orders", layer: "application"}, m + "orders/adapters/mysql", false},
		{"application platform", file{context: "orders", layer: "application"}, m + "platform/mysql", false},
		{"application other domain", file{context: "orders", layer: "application"}, m + "catalog/domain", false},
		{"application other ports", file{context: "orders", layer: "application"}, m + "inventory/ports", true},
		{"adapters platform", file{context: "orders", layer: "adapters"}, m + "platform/mysql", true},
		{"adapters other adapters", file{context: "orders", layer: "adapters"}, m + "catalog/adapters/mysql", false},
		{"ports application", file{context: "orders", layer: "ports"}, m + "orders/application", false},
	}
	for _, c := range cases {
		got := violation(c.f, c.imp) == ""
		if got != c.allowed {
			t.Errorf("%s: allowed=%v, want %v (%q)", c.name, got, c.allowed, violation(c.f, c.imp))
		}
	}
}
