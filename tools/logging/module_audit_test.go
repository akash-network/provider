package logging

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// rootLoggers are the functions returning loggers that carry no module.
var rootLoggers = map[string]bool{
	"pkg.akt.dev/go/util/ctxlog.LogcFromCtx":              true,
	"pkg.akt.dev/go/util/ctxlog.Logger":                   true,
	"github.com/akash-network/provider/tools/logging.New": true,
}

// TestLogsHaveModule fails when a log call goes through a logger taken from the
// context (ctxlog.LogcFromCtx, ctxlog.Logger) or created by logging.New without
// a "module" bound by With on the way: logging.Setup stores the root logger in
// the context without one.
//
// Loggers of unknown origin (parameters, results of other functions, variables
// and fields also assigned other loggers) are not audited.
func TestLogsHaveModule(t *testing.T) {
	root := repoRoot(t)

	pkgs, err := loadAuditPackages(root, "./...")
	require.NoError(t, err)
	require.NotEmpty(t, pkgs)

	var violations []string
	for _, p := range pkgs {
		violations = append(violations, newModuleAudit(p).run()...)
	}

	for i := range violations {
		violations[i] = strings.TrimPrefix(violations[i], root+"/")
	}
	sort.Strings(violations)

	require.Empty(t, violations, "log calls without a module:\n%s", strings.Join(violations, "\n"))
}

func TestModuleAuditFixture(t *testing.T) {
	checkAuditFixture(t, "./tools/logging/testdata/modules", func(p *auditPackage) []string {
		return newModuleAudit(p).run()
	})
}

type moduleAudit struct {
	pkg *auditPackage
	// vars and fields map logger variables and struct fields to the root
	// function every assignment to them comes from, when all of them lack a module.
	vars   map[*types.Var]string
	fields map[*types.Var]string
}

func newModuleAudit(p *auditPackage) *moduleAudit {
	a := &moduleAudit{pkg: p}
	a.collectAssignments()

	return a
}

// rootName returns "pkg.Func" when e calls one of rootLoggers, or "".
func (a *moduleAudit) rootName(e *ast.CallExpr) string {
	var id *ast.Ident
	switch f := e.Fun.(type) {
	case *ast.SelectorExpr:
		id = f.Sel
	case *ast.Ident:
		id = f
	default:
		return ""
	}

	fn, ok := a.pkg.TypesInfo.Uses[id].(*types.Func)
	if !ok || fn.Pkg() == nil || !rootLoggers[fn.Pkg().Path()+"."+fn.Name()] {
		return ""
	}

	return fn.Pkg().Name() + "." + fn.Name()
}

// origin returns the root function logger expression e comes from when no
// module is bound on the way, or "" when e has a module or an unknown origin.
func (a *moduleAudit) origin(e ast.Expr) string {
	info := a.pkg.TypesInfo

	switch x := e.(type) {
	case *ast.ParenExpr:
		return a.origin(x.X)
	case *ast.CallExpr:
		if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == withMethod && isLoggerType(info.TypeOf(sel.X)) {
			if literalKeys(x.Args)[KeyModule] {
				return ""
			}
			return a.origin(sel.X)
		}
		return a.rootName(x)
	case *ast.Ident:
		if v, ok := info.Uses[x].(*types.Var); ok {
			return a.vars[v]
		}
	case *ast.SelectorExpr:
		if sel := info.Selections[x]; sel != nil {
			if v, ok := sel.Obj().(*types.Var); ok && v.IsField() {
				return a.fields[v]
			}
		}
	}

	return ""
}

// collectAssignments fills vars and fields. Each pass recomputes them from the
// previous one, so loggers derived from variables declared later still resolve.
func (a *moduleAudit) collectAssignments() {
	info := a.pkg.TypesInfo

	for pass := 0; pass < 3; pass++ {
		vars := map[*types.Var]string{}
		fields := map[*types.Var]string{}
		mixed := map[*types.Var]bool{}

		record := func(m map[*types.Var]string, v *types.Var, rhs ast.Expr) {
			if v == nil || !isLoggerType(v.Type()) || mixed[v] {
				return
			}
			o := ""
			if rhs != nil {
				o = a.origin(rhs)
			}
			if o == "" || (m[v] != "" && m[v] != o) {
				mixed[v] = true
				delete(m, v)
				return
			}
			m[v] = o
		}

		lhsVar := func(lhs ast.Expr) (map[*types.Var]string, *types.Var) {
			switch lv := lhs.(type) {
			case *ast.Ident:
				obj := info.Defs[lv]
				if obj == nil {
					obj = info.Uses[lv]
				}
				v, _ := obj.(*types.Var)
				return vars, v
			case *ast.SelectorExpr:
				if sel := info.Selections[lv]; sel != nil {
					if v, ok := sel.Obj().(*types.Var); ok && v.IsField() {
						return fields, v
					}
				}
			}
			return nil, nil
		}

		for _, f := range a.pkg.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.AssignStmt:
					for i, lhs := range x.Lhs {
						var rhs ast.Expr
						if len(x.Lhs) == len(x.Rhs) {
							rhs = x.Rhs[i]
						}
						if m, v := lhsVar(lhs); m != nil {
							record(m, v, rhs)
						}
					}
				case *ast.ValueSpec:
					for i, name := range x.Names {
						if i < len(x.Values) && len(x.Names) == len(x.Values) {
							v, _ := info.Defs[name].(*types.Var)
							record(vars, v, x.Values[i])
						}
					}
				case *ast.CompositeLit:
					st, ok := typeUnderlyingStruct(info.TypeOf(x))
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
								record(fields, st.Field(i), kv.Value)
							}
						}
					}
				}
				return true
			})
		}

		a.vars, a.fields = vars, fields
	}
}

func typeUnderlyingStruct(t types.Type) (*types.Struct, bool) {
	if t == nil {
		return nil, false
	}
	st, ok := t.Underlying().(*types.Struct)

	return st, ok
}

// run returns a "file:line: logger from pkg.Func has no module" violation for
// every log call through a logger without a module.
func (a *moduleAudit) run() []string {
	var res []string
	info := a.pkg.TypesInfo

	for _, f := range a.pkg.Syntax {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !logMethods[sel.Sel.Name] || !isLoggerType(info.TypeOf(sel.X)) {
				return true
			}

			if root := a.origin(sel.X); root != "" {
				pos := a.pkg.Fset.Position(call.Pos())
				res = append(res, fmt.Sprintf("%s:%d: logger from %s has no module; bind one with With(%q, ...)",
					pos.Filename, pos.Line, root, KeyModule))
			}

			return true
		})
	}

	return res
}
