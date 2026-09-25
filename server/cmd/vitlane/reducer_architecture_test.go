package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestReducerOwnerAppsCannotImportAnotherOwner(t *testing.T) {
	owners := []string{"agencyorder", "procurement", "payment", "logistics"}
	for _, owner := range owners {
		root := filepath.Join("..", "..", "internal", "ordering", owner, "app")
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, e := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if e != nil {
				return e
			}
			for _, i := range f.Imports {
				value, _ := strconv.Unquote(i.Path.Value)
				for _, other := range owners {
					if other != owner && strings.Contains(value, "/ordering/"+other+"/") {
						t.Errorf("Owner app dependency: %s imports %s", path, value)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestReducerPoliciesStayOutOfStoreAndDeliveryLoops(t *testing.T) {
	root := filepath.Join("..", "..", "internal", "ordering", "process")
	paths, err := filepath.Glob(filepath.Join(root, "infra", "postgres", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, filepath.Join(root, "..", "procmsg", "queue", "worker.go"), filepath.Join(root, "..", "procmsg", "queue", "queue.go"))
	literalTransition := regexp.MustCompile(`(?i)\bSET\s+(?:status|stage|active_operation_id)\s*=\s*'`)
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch selector.Sel.Name {
					case "Admission", "ResolveOperationStep", "Reduce", "Decide", "CanAbandonCommandType":
						t.Errorf("workflow policy leaked into %s: %s", path, selector.Sel.Name)
					}
				}
			}
			if literal, ok := n.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				s, _ := strconv.Unquote(literal.Value)
				if strings.Contains(s, "UPDATE agency_order_processes") && literalTransition.MatchString(s) {
					t.Errorf("Store chooses a workflow transition: %s", path)
				}
			}
			return true
		})
	}
}
func TestReducerCriticalOwnerSQLUsesOnlyOwnedMutableResources(t *testing.T) {
	checks := []struct {
		file      string
		functions []string
		forbidden []string
	}{
		{"procurement/infra/postgres/manual_review.go", []string{"PrepareMerchantEffectFunding", "ResolveMerchantEffectFunding"}, []string{"payment_", "logistics_", "agency_order_processes", "order_workflow_"}},
		{"procurement/infra/postgres/cancel_live.go", []string{"CancelMerchantOrder", "RecordPlaced"}, []string{"payment_mo_", "logistics_", "agency_order_processes", "order_workflow_"}},
		{"procurement/infra/postgres/effect.go", nil, []string{"payment_", "logistics_", "agency_order_processes"}},
		{"payment/infra/postgres/instruction_gate.go", nil, []string{"agency_order_payment_instructions", "ConsumeInstruction("}},
		{"payment/infra/postgres/mo_funding.go", nil, []string{"merchant_orders", "logistics_", "order_workflow_mo_controls"}},
		{"payment/infra/postgres/mo_reauthorization.go", nil, []string{"merchant_orders", "logistics_", "order_workflow_mo_controls"}},
		{"payment/infra/postgres/mo_compensation.go", nil, []string{"merchant_orders", "logistics_", "order_workflow_mo_controls"}},
	}
	for _, check := range checks {
		path := filepath.Join("..", "..", "internal", "ordering", check.file)
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			selected := len(check.functions) == 0
			for _, name := range check.functions {
				selected = selected || fn.Name.Name == name
			}
			if !selected {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "GetQueueItem" || selector.Sel.Name == "getQueueItem") && fn.Name.Name != "RecordPlaced" {
						t.Errorf("workflow execution reused presentation query %s.%s", check.file, fn.Name.Name)
					}
				}
				literal, ok := n.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				s, _ := strconv.Unquote(literal.Value)
				for _, foreign := range check.forbidden {
					if strings.Contains(s, foreign) {
						t.Errorf("critical Owner SQL crosses boundary %s.%s: %s", check.file, fn.Name.Name, foreign)
					}
				}
				return true
			})
		}
	}
}
