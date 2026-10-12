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

// TestLogsCarryEntityIDs fails when a log call is made while an entity ID (lease,
// order, deployment, group or bid) is in lexical scope, but neither the call nor
// the logger's known With(...) chain carries an ID key (lid, oid, did, gid, bid).
// It also fails when a call repeats a key the logger already carries, which would
// emit the key twice.
//
// An ID is in scope when a variable declared before the call, in an enclosing
// block of the same function (parameters and receiver included), is an ID, or is
// a struct with an ID field, or has a no-argument method returning an ID.
//
// Calls where the ID is in scope but must not be logged (e.g. it is known to be
// invalid) are exempted with a "logaudit:ignore <reason>" comment on the call
// line or the line above.
func TestLogsCarryEntityIDs(t *testing.T) {
	root := repoRoot(t)

	pkgs, err := loadAuditPackages(root)
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

	require.Empty(t, violations, "log calls without an entity ID key while one is in scope:\n%s", strings.Join(violations, "\n"))
}

var (
	entityIDTypes = map[string]string{"LeaseID": KeyLeaseID, "OrderID": KeyOrderID, "DeploymentID": KeyDeploymentID, "GroupID": KeyGroupID, "BidID": KeyBidID}
	entityIDKeys  = map[string]bool{KeyLeaseID: true, KeyOrderID: true, KeyDeploymentID: true, KeyGroupID: true, KeyBidID: true}
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

// loadAuditPackages type-checks every package of the main module in root.
func loadAuditPackages(root string) ([]*auditPackage, error) {
	cmd := exec.Command("go", "list", "-e", "-export", "-compiled", "-deps", "-json", "./...")
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

// carriesID reports which entity ID a value of type t gives access to: t is an
// ID, has an ID field (directly or one struct level down), or has a no-argument
// method returning an ID.
func carriesID(t types.Type) string {
	return carriesIDDepth(t, 2)
}

func carriesIDDepth(t types.Type, depth int) string {
	if k := idKind(t); k != "" {
		return k
	}

	base := t
	if p, ok := base.(*types.Pointer); ok {
		base = p.Elem()
	}

	if st, ok := base.Underlying().(*types.Struct); ok && depth > 0 {
		for i := 0; i < st.NumFields(); i++ {
			ft := st.Field(i).Type()
			if k := idKind(ft); k != "" {
				return k
			}
			if _, isStruct := ft.Underlying().(*types.Struct); isStruct && depth > 1 {
				if k := carriesIDDepth(ft, depth-1); k != "" {
					return k
				}
			}
		}
	}

	ms := types.NewMethodSet(types.NewPointer(base))
	if types.IsInterface(base) {
		ms = types.NewMethodSet(base)
	}
	for i := 0; i < ms.Len(); i++ {
		sig, ok := ms.At(i).Type().(*types.Signature)
		if ok && sig.Params().Len() == 0 && sig.Results().Len() == 1 {
			if k := idKind(sig.Results().At(0).Type()); k != "" {
				return k
			}
		}
	}

	return ""
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
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "With" {
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

// idsInScope returns the entity-ID-bearing variables visible at pos inside fn.
func (a *entityAudit) idsInScope(fn *ast.FuncDecl, pos token.Pos) []string {
	scope := a.pkg.Types.Scope().Innermost(pos)
	fnScope := a.pkg.TypesInfo.Scopes[fn.Type]

	var res []string
	for s := scope; s != nil; s = s.Parent() {
		for _, name := range s.Names() {
			v, ok := s.Lookup(name).(*types.Var)
			if !ok || v.Pos() >= pos || name == "_" {
				continue
			}
			if k := carriesID(v.Type()); k != "" {
				res = append(res, k+"="+name)
			}
		}
		if s == fnScope {
			break
		}
	}
	sort.Strings(res)

	return res
}

func (a *entityAudit) run() []string {
	var res []string
	info := a.pkg.TypesInfo

	for _, f := range a.pkg.Syntax {
		ignored := map[int]bool{}
		for _, cg := range f.Comments {
			for _, c := range cg.List {
				if strings.Contains(c.Text, "logaudit:ignore") {
					ignored[a.pkg.Fset.Position(c.Pos()).Line] = true
				}
			}
		}

		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !logMethods[sel.Sel.Name] || !isLoggerType(info.TypeOf(sel.X)) {
					return true
				}

				pos := a.pkg.Fset.Position(call.Pos())
				if ignored[pos.Line] || ignored[pos.Line-1] {
					return true
				}

				keys := literalKeys(call.Args[1:])
				bound := a.boundKeys(sel.X)

				var dups []string
				for k := range keys {
					if bound[k] && k != KeyModule {
						dups = append(dups, k)
					}
				}
				if len(dups) > 0 {
					sort.Strings(dups)
					res = append(res, fmt.Sprintf("%s:%d: %s %s: key(s) %s already bound by the logger's With",
						pos.Filename, pos.Line, fn.Name.Name, types.ExprString(call.Args[0]), strings.Join(dups, ",")))
				}

				for k := range bound {
					keys[k] = true
				}
				for k := range keys {
					if entityIDKeys[k] {
						return true
					}
				}

				ids := a.idsInScope(fn, call.Pos())
				if len(ids) == 0 {
					return true
				}

				res = append(res, fmt.Sprintf("%s:%d: %s %s: in scope [%s], logged without lid/oid/did/gid/bid",
					pos.Filename, pos.Line, fn.Name.Name, types.ExprString(call.Args[0]), strings.Join(ids, " ")))

				return true
			})
		}
	}

	return res
}
