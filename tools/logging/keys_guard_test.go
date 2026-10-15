package logging

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

var (
	canonicalKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

	// non-canonical spellings with a canonical replacement (see _docs/logging.md)
	bannedKeys = map[string]string{
		"lease":        KeyLeaseID,
		"lease_id":     KeyLeaseID,
		"lease_ns":     KeyNamespace,
		"order":        KeyOrderID,
		"order_id":     KeyOrderID,
		"deployment":   KeyDeploymentID,
		"group":        KeyGroupID,
		"error":        KeyErr,
		"namespace":    KeyNamespace,
		"cmp":          KeyModule,
		"component":    KeyModule,
		"op":           KeyModule,
		"operator":     KeyModule,
		"cnt":          KeyCount,
		"qty":          KeyCount,
		"quantity":     KeyCount,
		"host":         KeyHostname,
		"addr":         "owner|provider|listen_addr",
		"address":      "listen_addr|pci_addr",
		"service_name": KeyService,
	}

	logMethods = map[string]bool{"Debug": true, "Info": true, "Warn": true, "Error": true}
	withMethod = "With"

	guardSkipDirs = map[string]bool{
		".git": true, ".cache": true, ".idea": true, ".superpowers": true, "dist": true,
		"vendor": true, "mocks": true, "node_modules": true,
	}
)

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod")) // nolint: gosec
		if err == nil && strings.Contains(string(data), "module github.com/akash-network/provider\n") {
			return dir
		}

		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "repository root not found")
		dir = parent
	}
}

// isMessage reports whether expr looks like a log message: a string literal or fmt.Sprint*(...).
func isMessage(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := sel.X.(*ast.Ident)
		return ok && pkg.Name == "fmt" && strings.HasPrefix(sel.Sel.Name, "Sprint")
	}

	return false
}

func checkKeyVals(fset *token.FileSet, call *ast.CallExpr, kv []ast.Expr, isWith bool) []string {
	var res []string

	pos := fset.Position(call.Pos())
	report := func(key, rule string) {
		res = append(res, fmt.Sprintf("%s:%d: key %q: %s", pos.Filename, pos.Line, key, rule))
	}

	spread := call.Ellipsis.IsValid()
	if !spread && len(kv)%2 != 0 {
		report("", "odd number of key/value arguments")
	}

	for i := 0; i < len(kv); i += 2 {
		if spread && i == len(kv)-1 {
			break
		}

		lit, ok := kv[i].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}

		key, err := strconv.Unquote(lit.Value)
		if err != nil {
			continue
		}

		if !canonicalKey.MatchString(key) {
			report(key, "must be snake_case")
		}

		if repl, banned := bannedKeys[key]; banned {
			report(key, "use "+strconv.Quote(repl))
		}

		if key == KeyModule && !isWith {
			report(key, "set module only via With")
		}
	}

	return res
}

func TestLogKeysAreCanonical(t *testing.T) {
	root := repoRoot(t)

	var violations []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(root, path)

		if d.IsDir() {
			if guardSkipDirs[d.Name()] || rel == filepath.Join("pkg", "client") {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, ".pb.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			switch {
			case sel.Sel.Name == withMethod:
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					violations = append(violations, checkKeyVals(fset, call, call.Args, true)...)
				}
			case logMethods[sel.Sel.Name] && isMessage(call.Args[0]):
				violations = append(violations, checkKeyVals(fset, call, call.Args[1:], false)...)
			}

			return true
		})

		return nil
	})
	require.NoError(t, err)

	for i := range violations {
		violations[i] = strings.TrimPrefix(violations[i], root+string(filepath.Separator))
	}

	require.Empty(t, violations, "non-canonical log keys:\n%s", strings.Join(violations, "\n"))
}
