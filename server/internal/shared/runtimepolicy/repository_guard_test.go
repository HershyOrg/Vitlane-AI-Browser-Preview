package runtimepolicy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeBoundariesCannotBypassManagedClientsOrContexts(t *testing.T) {
	root := serverRoot(t)
	allowedContextRoots := map[string]int{
		// 3번째 root는 `vitlane paypal-binding` 서브커맨드의 단독 entrypoint다.
		"cmd/vitlane/main.go":                       3,
		"internal/shared/infra/postgres/migrate.go": 1,
	}
	seenContextRoots := map[string]int{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
			strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		set := token.NewFileSet()
		file, parseErr := parser.ParseFile(set, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		imports := importAliases(file)
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				identifier, _ := selector.X.(*ast.Ident)
				importPath := ""
				if identifier != nil {
					importPath = imports[identifier.Name]
				}
				switch {
				case importPath == "context" &&
					(selector.Sel.Name == "Background" || selector.Sel.Name == "TODO"):
					seenContextRoots[relative]++
					if seenContextRoots[relative] > allowedContextRoots[relative] {
						t.Errorf("%s creates an unapproved root context", relative)
					}
				case importPath == "database/sql" && selector.Sel.Name == "Open" &&
					relative != "internal/shared/infra/postgres/database.go":
					t.Errorf("%s opens a database outside shared postgres", relative)
				case importPath == "net/http" &&
					(selector.Sel.Name == "Get" || selector.Sel.Name == "Head" ||
						selector.Sel.Name == "Post" || selector.Sel.Name == "PostForm"):
					t.Errorf("%s uses an unmanaged HTTP shortcut", relative)
				case importPath == "net" &&
					(selector.Sel.Name == "Dial" || selector.Sel.Name == "DialTimeout"):
					t.Errorf("%s performs an unmanaged network dial", relative)
				case strings.HasSuffix(importPath, "/shared/infra/httpclient") &&
					selector.Sel.Name == "Do":
					// Reviewed outbound call boundary.
				case selector.Sel.Name == "Do" && !looksLikeSyncOnce(selector.X) &&
					relative != "internal/shared/infra/httpclient/client.go":
					t.Errorf("%s performs HTTP Do outside shared httpclient", relative)
				case importPath == "github.com/ethereum/go-ethereum/ethclient" &&
					strings.HasPrefix(selector.Sel.Name, "Dial"):
					t.Errorf("%s dials EVM without the managed HTTP transport", relative)
				}
			case *ast.CompositeLit:
				selector, ok := value.Type.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				identifier, _ := selector.X.(*ast.Ident)
				if identifier == nil {
					return true
				}
				importPath := imports[identifier.Name]
				if importPath == "net/http" && selector.Sel.Name == "Client" &&
					relative != "internal/shared/infra/httpclient/client.go" {
					t.Errorf("%s constructs an unmanaged HTTP client", relative)
				}
				if importPath == "net" && selector.Sel.Name == "Dialer" &&
					relative != "internal/shared/infra/httpclient/client.go" {
					t.Errorf("%s constructs an unmanaged network dialer", relative)
				}
			case *ast.SelectorExpr:
				identifier, _ := value.X.(*ast.Ident)
				if identifier != nil && imports[identifier.Name] == "net/http" &&
					(value.Sel.Name == "DefaultClient" || value.Sel.Name == "DefaultTransport") {
					t.Errorf("%s uses an unmanaged default HTTP client or transport", relative)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, expected := range allowedContextRoots {
		if seenContextRoots[path] != expected {
			t.Errorf("%s root context count=%d want=%d", path, seenContextRoots[path], expected)
		}
	}
}

func TestExternalRuntimeBoundaryInventoryMarkers(t *testing.T) {
	root := serverRoot(t)
	boundaries := map[string][]string{
		"internal/account/infra/googleoidc/provider.go": {
			"oauth2.HTTPClient", "GOOGLE_CODE_EXCHANGE_UNKNOWN",
		},
		"internal/curation/research/infra/dealfeed/recovery.go":                       {"sharedhttpclient.Do", "ReadOnly"},
		"internal/account/infra/dojang/provider.go":                                   {"rpc.WithHTTPClient"},
		"internal/curation/research/infra/shopifyucp/client_v2.go":                    {"sharedhttpclient.Do", "ReadOnly"},
		"internal/curation/intelligence/managedrunner/infra/openaiapi/client.go":      {"sharedhttpclient.Do", "ExternalEffect"},
		"internal/ordering/payment/infra/paypal/client.go":                            {"sharedhttpclient.Do", "ExternalEffect"},
		"internal/curation/intelligence/managedrunner/infra/intelligence/provider.go": {"MarkUnknown", "Finalizer"},
		"internal/ordering/payment/giwa/infra/evm/gateway.go":                         {"rpc.WithHTTPClient"},
		"internal/account/app/kyc.go":                                                 {"finalizationContext", "KYC_PROVIDER_EFFECT_UNKNOWN"},
		"internal/curation/intelligence/iface/worker/worker.go":                       {"Attempt.DeadlineAt", "tickTimeout"},
		"internal/curation/intelligence/app/service.go":                               {"finalizer.Context", "CloseAttempt(finalizeContext"},
	}
	for path, markers := range boundaries {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range markers {
			if !strings.Contains(string(content), marker) {
				t.Errorf("runtime boundary %s is missing %q", path, marker)
			}
		}
	}
}

func TestHTTPErrorHelpersPreserveFaultTaxonomy(t *testing.T) {
	root := serverRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if entry.IsDir() || !strings.Contains(relative, "/iface/http/") ||
			!strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		set := token.NewFileSet()
		file, parseErr := parser.ParseFile(set, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		imports := importAliases(file)
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil ||
				!strings.HasPrefix(function.Name.Name, "write") ||
				!strings.HasSuffix(function.Name.Name, "Error") ||
				!hasErrorParameter(function) {
				continue
			}
			preservesFault := hasSharedFaultAsCall(function.Body, imports)
			if !preservesFault {
				t.Errorf("%s %s hides shared fault classifications", relative, function.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A later errors.As call must not erase an earlier shared fault.As match.
func hasSharedFaultAsCall(body *ast.BlockStmt, imports map[string]string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if found {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "As" {
			return true
		}
		identifier, _ := selector.X.(*ast.Ident)
		found = identifier != nil && imports[identifier.Name] == "github.com/vitlane/vitlane/server/internal/shared/fault"
		return !found
	})
	return found
}

func TestFaultTaxonomyGuardKeepsEarlierMatch(t *testing.T) {
	imports := map[string]string{"fault": "github.com/vitlane/vitlane/server/internal/shared/fault", "errors": "errors"}
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`fault.As(err); errors.As(err, &pg)`, true},
		{`errors.As(err, &pg); fault.As(err)`, true},
		{`errors.As(err, &pg)`, false},
		{`httpapi.WriteError(w, 422, "UNKNOWN", "error")`, false},
	} {
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", "package fixture; func writeError(){"+tc.body+"}", 0)
		if err != nil {
			t.Fatal(err)
		}
		function := file.Decls[0].(*ast.FuncDecl)
		if got := hasSharedFaultAsCall(function.Body, imports); got != tc.want {
			t.Fatalf("%s: got %t want %t", tc.body, got, tc.want)
		}
	}
}

func TestInternalHTTPFallbacksPreserveClassifiedFaults(t *testing.T) {
	root := serverRoot(t)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, _ := filepath.Rel(root, path)
		relative = filepath.ToSlash(relative)
		if entry.IsDir() || !strings.Contains(relative, "/iface/http/") ||
			!strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		set := token.NewFileSet()
		file, parseErr := parser.ParseFile(set, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		imports := importAliases(file)
		ast.Inspect(file, func(node ast.Node) bool {
			statement, ok := node.(*ast.IfStmt)
			if !ok || !containsErrorIdentifier(statement.Cond) ||
				!containsStringLiteral(statement.Body, "INTERNAL_ERROR") {
				return true
			}
			if !containsSelectorCall(
				statement.Body, imports,
				"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi",
				"WriteFaultIfClassified",
			) && !containsSelectorCall(
				statement.Body, imports,
				"github.com/vitlane/vitlane/server/internal/shared/fault", "As",
			) {
				position := set.Position(statement.Pos())
				t.Errorf("%s:%d hides a classified fault behind INTERNAL_ERROR", relative, position.Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsErrorIdentifier(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(candidate ast.Node) bool {
		identifier, ok := candidate.(*ast.Ident)
		if ok {
			name := strings.ToLower(identifier.Name)
			found = name == "err" || strings.HasSuffix(name, "error") ||
				strings.HasSuffix(name, "err")
		}
		return !found
	})
	return found
}

func containsStringLiteral(node ast.Node, expected string) bool {
	found := false
	ast.Inspect(node, func(candidate ast.Node) bool {
		if found {
			return false
		}
		literal, ok := candidate.(*ast.BasicLit)
		if ok && literal.Kind == token.STRING {
			value, _ := strconv.Unquote(literal.Value)
			if value == expected {
				found = true
			}
		}
		return !found
	})
	return found
}

func containsSelectorCall(
	node ast.Node,
	imports map[string]string,
	expectedImport string,
	expectedCall string,
) bool {
	found := false
	ast.Inspect(node, func(candidate ast.Node) bool {
		if found {
			return false
		}
		call, ok := candidate.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, _ := selector.X.(*ast.Ident)
		if identifier != nil &&
			imports[identifier.Name] == expectedImport &&
			selector.Sel.Name == expectedCall {
			found = true
		}
		return !found
	})
	return found
}

func hasErrorParameter(function *ast.FuncDecl) bool {
	if function.Type.Params == nil {
		return false
	}
	for _, field := range function.Type.Params.List {
		identifier, ok := field.Type.(*ast.Ident)
		if ok && identifier.Name == "error" {
			return true
		}
	}
	return false
}

func looksLikeSyncOnce(expression ast.Expr) bool {
	switch value := expression.(type) {
	case *ast.Ident:
		return strings.Contains(strings.ToLower(value.Name), "once")
	case *ast.SelectorExpr:
		return strings.Contains(strings.ToLower(value.Sel.Name), "once") ||
			looksLikeSyncOnce(value.X)
	default:
		return false
	}
}

func importAliases(file *ast.File) map[string]string {
	aliases := map[string]string{}
	for _, spec := range file.Imports {
		path, _ := strconv.Unquote(spec.Path.Value)
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		aliases[name] = path
	}
	return aliases
}

func serverRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "../../.."))
}
