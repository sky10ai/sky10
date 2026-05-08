package bridge

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandlersValidateBeforeUse enforces handler-discipline rule 5:
// the first non-trivial statement of every envelope handler must be a
// validation step — a call to a parse* or validate* function, a
// direct json.Unmarshal call, or a return statement (for trivial
// handlers with no payload to validate).
//
// The test scans every Go file in subpackages of pkg/sandbox/bridge/
// (the bridge root itself is plumbing, not handlers) and inspects any
// function or method whose name starts with "handle". Failures point
// at the file:line where the wrong shape was detected.
//
// This is intentionally a conservative check: a clever developer
// could defeat it by renaming functions or hiding validation behind
// an abstraction. The goal is to make the *wrong shape* visibly fail
// in CI, not to formally prove correctness.
func TestHandlersValidateBeforeUse(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()

	walkErr := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// Only scan files in subpackages; skip the bridge root.
		if filepath.Dir(path) == "." {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if !strings.HasPrefix(fd.Name.Name, "handle") {
				continue
			}
			if fd.Body == nil || len(fd.Body.List) == 0 {
				continue
			}
			first := fd.Body.List[0]
			if !looksLikeValidation(first) {
				pos := fset.Position(first.Pos())
				t.Errorf("%s:%d %s: first non-trivial statement must validate (call parse*, validate*, or json.Unmarshal); see docs/work/current/sandbox-bridge/handler-discipline.md rule 5",
					path, pos.Line, fd.Name.Name)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk failed: %v", walkErr)
	}
}

// looksLikeValidation returns true when the supplied statement either
// is a return statement or contains a call to a function whose name
// starts with parse or validate, or to json.Unmarshal. Inspecting the
// statement (rather than the immediate top-level expression) catches
// the common patterns:
//
//   - p, err := parseFooParams(env.Payload)
//   - if err := parseFooParams(env.Payload); err != nil { ... }
//   - if err := json.Unmarshal(env.Payload, &p); err != nil { ... }
func looksLikeValidation(stmt ast.Stmt) bool {
	if _, ok := stmt.(*ast.ReturnStmt); ok {
		return true
	}
	var found bool
	ast.Inspect(stmt, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if isValidationName(fn.Name) {
				found = true
				return false
			}
		case *ast.SelectorExpr:
			if isValidationSelector(fn) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func isValidationName(name string) bool {
	return strings.HasPrefix(name, "parse") || strings.HasPrefix(name, "validate")
}

func isValidationSelector(sel *ast.SelectorExpr) bool {
	if sel.Sel == nil {
		return false
	}
	switch sel.Sel.Name {
	case "Unmarshal":
		// json.Unmarshal or any other Unmarshal — broadly intent is
		// "parse the wire bytes," which counts as validation under
		// this rule.
		return true
	}
	return isValidationName(sel.Sel.Name)
}

// TestLooksLikeValidationClassifies verifies the AST helper used by
// TestHandlersValidateBeforeUse correctly distinguishes validation
// statements from business-logic statements. Without this, the arch
// test could silently pass because the helper accepts everything.
func TestLooksLikeValidationClassifies(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		body   string
		expect bool
	}{
		{"return only", `return nil, nil`, true},
		{"parse assign", `params, err := parseFooParams(env.Payload); _, _ = params, err`, true},
		{"validate assign", `if err := validateThing(x); err != nil { return nil, err }`, true},
		{"json unmarshal", `if err := json.Unmarshal(env.Payload, &p); err != nil { return nil, err }`, true},
		{"plain method call", `p.DoBusinessLogic()`, false},
		{"variable decl", `x := 42`, false},
		{"struct call", `result := backend.Call(env.AgentID)`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "package x\nfunc f() {\n" + tc.body + "\n}\n"
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "x.go", src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			fn := file.Decls[0].(*ast.FuncDecl)
			if len(fn.Body.List) == 0 {
				t.Fatalf("no body for %q", tc.name)
			}
			got := looksLikeValidation(fn.Body.List[0])
			if got != tc.expect {
				t.Fatalf("looksLikeValidation(%s) = %v, want %v", tc.name, got, tc.expect)
			}
		})
	}
}
