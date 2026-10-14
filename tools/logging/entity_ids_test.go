package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLogEntityIDKeys audits the key/value pairs of log and With calls that carry
// an entity ID (lease, order, deployment, group or bid). It fails when
//   - an ID value is logged under a key other than its own (lid, oid, did, gid, bid),
//   - an ID key carries a value that is not of its ID type,
//   - an ID key is already bound by the logger's known With(...) chain, which
//     would emit it twice.
//
// Entries without ID keys or values are not audited: logging an ID is never required.
func TestLogEntityIDKeys(t *testing.T) {
	root := repoRoot(t)

	pkgs, err := loadAuditPackages(root, "./...")
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	violations := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		violations = append(violations, newEntityAudit(p).run()...)
	}

	for i := range violations {
		violations[i] = strings.TrimPrefix(violations[i], root+"/")
	}
	sort.Strings(violations)

	require.Empty(t, violations, "misused entity ID log keys:\n%s", strings.Join(violations, "\n"))
}

// TestEntityIDAuditFixture runs the audit on testdata/entityids, where every
// line that must be reported carries a `// want` comment quoting part of the violation.
func TestEntityIDAuditFixture(t *testing.T) {
	pkgs, err := loadAuditPackages(repoRoot(t), "./tools/logging/testdata/entityids")
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	p := pkgs[0]

	want := map[string]string{}
	for _, f := range p.Syntax {
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if s, ok := strings.CutPrefix(c.Text, "// want "); ok {
					w, err := strconv.Unquote(s)
					require.NoError(t, err, c.Text)
					pos := p.Fset.Position(c.Pos())
					want[fmt.Sprintf("%s:%d", pos.Filename, pos.Line)] = w
				}
			}
		}
	}
	require.NotEmpty(t, want)

	for _, v := range newEntityAudit(p).run() {
		loc, _, _ := strings.Cut(v, ": ")
		w, ok := want[loc]
		if !ok {
			t.Errorf("unexpected violation: %s", v)
			continue
		}
		delete(want, loc)
		if !strings.Contains(v, w) {
			t.Errorf("violation %q does not contain %q", v, w)
		}
	}

	for loc, w := range want {
		t.Errorf("%s: violation %q not reported", loc, w)
	}
}

var (
	// entityIDTypes maps an ID type name to its key; entityIDKeys is the reverse.
	entityIDTypes = map[string]string{"LeaseID": KeyLeaseID, "OrderID": KeyOrderID, "DeploymentID": KeyDeploymentID, "GroupID": KeyGroupID, "BidID": KeyBidID}
	entityIDKeys  = map[string]string{KeyLeaseID: "LeaseID", KeyOrderID: "OrderID", KeyDeploymentID: "DeploymentID", KeyGroupID: "GroupID", KeyBidID: "BidID"}
)

// auditPackage is a provider package type-checked from source; its dependencies
// are imported from the toolchain's export data.
type auditPackage struct {
	Fset      *token.FileSet
	Syntax    []*ast.File
	Types     *types.Package
	TypesInfo *types.Info
}

type listedPackage struct {
	ImportPath      string
	Dir             string
	Export          string
	Module          *struct{ Main bool }
	CompiledGoFiles []string
	DepOnly         bool
	Error           *struct{ Err string }
}

// loadAuditPackages type-checks the main module packages in root matching pattern.
func loadAuditPackages(root, pattern string) ([]*auditPackage, error) {
	cmd := exec.Command("go", "list", "-e", "-export", "-compiled", "-deps", "-json", pattern) // nolint: gosec
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, stderr.String())
	}

	exports := map[string]string{}
	var roots []listedPackage
	for dec := json.NewDecoder(bytes.NewReader(out)); ; {
		var lp listedPackage
		if err := dec.Decode(&lp); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if lp.Error != nil {
			return nil, fmt.Errorf("%s: %s", lp.ImportPath, lp.Error.Err)
		}
		exports[lp.ImportPath] = lp.Export
		if !lp.DepOnly && lp.Module != nil && lp.Module.Main {
			roots = append(roots, lp)
		}
	}

	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file) // nolint: gosec
	})

	res := make([]*auditPackage, 0, len(roots))
	for _, lp := range roots {
		p := &auditPackage{
			Fset: fset,
			TypesInfo: &types.Info{
				Types:      map[ast.Expr]types.TypeAndValue{},
				Defs:       map[*ast.Ident]types.Object{},
				Uses:       map[*ast.Ident]types.Object{},
				Selections: map[*ast.SelectorExpr]*types.Selection{},
				Scopes:     map[ast.Node]*types.Scope{},
			},
		}

		for _, name := range lp.CompiledGoFiles {
			if !filepath.IsAbs(name) {
				name = filepath.Join(lp.Dir, name)
			}
			// cgo-generated files live in the build cache; audit only the module's own sources
			if !strings.HasPrefix(name, root) {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			p.Syntax = append(p.Syntax, f)
		}

		conf := types.Config{Importer: imp}
		p.Types, err = conf.Check(lp.ImportPath, fset, p.Syntax, p.TypesInfo)
		if err != nil {
			return nil, fmt.Errorf("type-checking %s: %w", lp.ImportPath, err)
		}
		res = append(res, p)
	}

	return res, nil
}

type entityAudit struct {
	pkg       *auditPackage
	fieldKeys map[*types.Var]map[string]bool
	varKeys   map[*types.Var]map[string]bool
	visiting  map[ast.Expr]bool
}

func newEntityAudit(p *auditPackage) *entityAudit {
	a := &entityAudit{
		pkg:       p,
		fieldKeys: map[*types.Var]map[string]bool{},
		varKeys:   map[*types.Var]map[string]bool{},
		visiting:  map[ast.Expr]bool{},
	}
	a.collectLoggerAssignments()

	return a
}

// idKind returns the canonical key for an entity ID type, or "".
func idKind(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	if !ok || n.Obj().Pkg() == nil || !strings.HasPrefix(n.Obj().Pkg().Path(), "pkg.akt.dev/go/node/") {
		return ""
	}

	return entityIDTypes[n.Obj().Name()]
}

func isLoggerType(t types.Type) bool {
	return t != nil && types.TypeString(t, nil) == "cosmossdk.io/log.Logger"
}

func literalKeys(args []ast.Expr) map[string]bool {
	res := map[string]bool{}
	for i := 0; i+1 < len(args); i += 2 {
		if lit, ok := args[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			k, _ := strconv.Unquote(lit.Value)
			res[k] = true
		}
	}

	return res
}

// boundKeys returns the keys known to be attached to logger expression e.
func (a *entityAudit) boundKeys(e ast.Expr) map[string]bool {
	if a.visiting[e] {
		return nil
	}
	a.visiting[e] = true
	defer delete(a.visiting, e)

	res := map[string]bool{}
	switch x := e.(type) {
	case *ast.ParenExpr:
		return a.boundKeys(x.X)
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == withMethod {
			for k := range literalKeys(x.Args) {
				res[k] = true
			}
			for k := range a.boundKeys(sel.X) {
				res[k] = true
			}
		}
	case *ast.Ident:
		if v, ok := a.pkg.TypesInfo.Uses[x].(*types.Var); ok {
			return a.varKeys[v]
		}
	case *ast.SelectorExpr:
		if sel := a.pkg.TypesInfo.Selections[x]; sel != nil {
			if v, ok := sel.Obj().(*types.Var); ok && v.IsField() {
				return a.fieldKeys[v]
			}
		}
	}

	return res
}

func (a *entityAudit) recordLogger(m map[*types.Var]map[string]bool, v *types.Var, rhs ast.Expr) {
	if v == nil || !isLoggerType(v.Type()) || len(m[v]) > 0 {
		return
	}
	m[v] = a.boundKeys(rhs)
}

// collectLoggerAssignments records the With keys of loggers assigned to local
// variables and struct fields (first assignment wins).
func (a *entityAudit) collectLoggerAssignments() {
	info := a.pkg.TypesInfo
	for pass := 0; pass < 3; pass++ {
		for _, f := range a.pkg.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.AssignStmt:
					for i, lhs := range x.Lhs {
						if i >= len(x.Rhs) {
							break
						}
						switch lv := lhs.(type) {
						case *ast.Ident:
							obj := info.Defs[lv]
							if obj == nil {
								obj = info.Uses[lv]
							}
							if v, ok := obj.(*types.Var); ok {
								a.recordLogger(a.varKeys, v, x.Rhs[i])
							}
						case *ast.SelectorExpr:
							if sel := info.Selections[lv]; sel != nil {
								if v, ok := sel.Obj().(*types.Var); ok && v.IsField() {
									a.recordLogger(a.fieldKeys, v, x.Rhs[i])
								}
							}
						}
					}
				case *ast.CompositeLit:
					t := info.TypeOf(x)
					if t == nil {
						return true
					}
					st, ok := t.Underlying().(*types.Struct)
					if !ok {
						return true
					}
					for _, elt := range x.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						name, ok := kv.Key.(*ast.Ident)
						if !ok {
							continue
						}
						for i := 0; i < st.NumFields(); i++ {
							if st.Field(i).Name() == name.Name {
								a.recordLogger(a.fieldKeys, st.Field(i), kv.Value)
							}
						}
					}
				}
				return true
			})
		}
	}
}

// run returns a "file:line: key "k": rule" violation for every misused ID key/value pair.
func (a *entityAudit) run() []string {
	var res []string
	info := a.pkg.TypesInfo
	pkgName := func(p *types.Package) string { return p.Name() }

	for _, f := range a.pkg.Syntax {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !isLoggerType(info.TypeOf(sel.X)) {
				return true
			}

			kv := call.Args
			switch {
			case sel.Sel.Name == withMethod:
			case logMethods[sel.Sel.Name]:
				kv = call.Args[1:]
			default:
				return true
			}

			pos := a.pkg.Fset.Position(call.Pos())
			report := func(key, rule string) {
				res = append(res, fmt.Sprintf("%s:%d: key %q: %s", pos.Filename, pos.Line, key, rule))
			}

			bound := a.boundKeys(sel.X)
			for i := 0; i+1 < len(kv); i += 2 {
				lit, ok := kv[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				key, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}

				vt := info.TypeOf(kv[i+1])
				vkey := idKind(vt)
				switch {
				case vkey != "" && vkey != key:
					report(key, fmt.Sprintf("%s value must use key %q", entityIDKeys[vkey], vkey))
				case entityIDKeys[key] != "" && vkey != key:
					report(key, fmt.Sprintf("must carry a %s, got %s", entityIDKeys[key], types.TypeString(vt, pkgName)))
				case entityIDKeys[key] != "" && bound[key]:
					report(key, "already bound by the logger's With")
				}
			}

			return true
		})
	}

	return res
}
