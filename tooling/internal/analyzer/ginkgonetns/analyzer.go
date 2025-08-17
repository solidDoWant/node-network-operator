package ginkgonetns

import (
	"go/ast"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const Comment = "vet: ginko-netns-check"

var Analyzer = &analysis.Analyzer{
	Name:     "ginkgonetns",
	Doc:      "check that all Ginkgo nodes use network namespace wrapper",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

// Ginkgo node functions that require the wrapper
var ginkgoNodes = map[string]struct{}{
	"BeforeEach":              {},
	"JustBeforeEach":          {},
	"AfterEach":               {},
	"JustAfterEach":           {},
	"BeforeAll":               {},
	"AfterAll":                {},
	"It":                      {},
	"Specify":                 {},
	"BeforeSuite":             {},
	"AfterSuite":              {},
	"SynchronizedBeforeSuite": {},
	"SynchronizedAfterSuite":  {},
}

func run(pass *analysis.Pass) (any, error) {
	// Only analyze test packages or packages that import Ginkgo
	if !shouldAnalyzePackage(pass) {
		return nil, nil
	}

	providedInspector, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, nil // Not using the inspector, nothing to do
	}

	nodeFilter := []ast.Node{
		(*ast.CallExpr)(nil),
	}

	providedInspector.Preorder(nodeFilter, func(n ast.Node) {
		call := n.(*ast.CallExpr)

		// Check if this is a call to a Ginkgo node function
		if !isGinkgoNodeCall(call) {
			return
		}

		// Get the function name
		funcName := getFunctionName(call)
		if funcName == "" {
			return
		}

		// Check if this call has a function literal argument
		funcLit := extractFunctionLiteral(call)
		if funcLit == nil {
			// Some nodes like Pending specs don't require function literals
			return
		}

		// Check if the function literal calls our wrapper or setup function
		if !callsNetworkNamespaceSetup(funcLit) {
			pass.Reportf(call.Pos(), "Ginkgo node '%s' must use withTestNetworkNamespace() wrapper", funcName)
		}
	})

	return nil, nil
}

// shouldAnalyzePackage determines if a package should be analyzed by checking
// for a special comment marker in the package files
func shouldAnalyzePackage(pass *analysis.Pass) bool {
	// Check if it's a test package (ends with _test)
	if strings.HasSuffix(pass.Pkg.Name(), "_test") {
		return true
	}

	// Check if package imports Ginkgo
	if !isDependencyImported(pass.Pkg.Imports()) {
		return false
	}

	// Check for the special comment marker in any file in the package
	for _, file := range pass.Files {
		if file.Comments == nil {
			continue
		}

		for _, commentGroup := range file.Comments {
			for _, comment := range commentGroup.List {
				// Look for comment like "//ginkgo:analyze" or "// ginkgo:analyze"
				text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
				if text == Comment {
					return true
				}
			}
		}
	}

	return false
}

func isDependencyImported(importedPackages []*types.Package) bool {
	packagePrefixes := []string{"github.com/onsi/ginkgo", "github.com/onsi/gomega"}
	return slices.ContainsFunc(importedPackages, func(importedPackage *types.Package) bool {
		return slices.ContainsFunc(packagePrefixes, func(prefix string) bool {
			return strings.HasPrefix(importedPackage.Path(), prefix)
		})
	})
}

// isGinkgoNodeCall checks if the call expression is calling a Ginkgo node function
func isGinkgoNodeCall(call *ast.CallExpr) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		// Direct call like It("test", func() {})
		_, exists := ginkgoNodes[fun.Name]
		return exists
	case *ast.SelectorExpr:
		// Qualified call like ginkgo.It("test", func() {})
		_, exists := ginkgoNodes[fun.Sel.Name]
		return exists
	}
	return false
}

// getFunctionName extracts the function name from a call expression
func getFunctionName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// extractFunctionLiteral finds the function literal argument in a Ginkgo node call
func extractFunctionLiteral(call *ast.CallExpr) *ast.FuncLit {
	// Ginkgo nodes typically have the pattern: NodeName(description, [decorators...], func() {})
	// The function literal is usually the last argument
	for i := len(call.Args) - 1; i >= 0; i-- {
		if funcLit, ok := call.Args[i].(*ast.FuncLit); ok {
			return funcLit
		}
	}
	return nil
}

// callsNetworkNamespaceSetup checks if the function literal calls our required setup
func callsNetworkNamespaceSetup(funcLit *ast.FuncLit) bool {
	if funcLit.Body == nil {
		return false
	}

	// Check if this function was created by withNetworkNamespace wrapper
	return isWrappedFunction(funcLit)
}

// isWrappedFunction tries to detect if this function literal was created by our wrapper
func isWrappedFunction(funcLit *ast.FuncLit) bool {
	// This is heuristic - look for patterns that suggest the function was wrapped
	if funcLit.Body == nil || len(funcLit.Body.List) == 0 {
		return false
	}

	// Look for runtime.LockOSThread() call as first statement
	if callStmt, ok := funcLit.Body.List[0].(*ast.ExprStmt); ok {
		if call, ok := callStmt.X.(*ast.CallExpr); ok {
			if isCallToFunction(call, "runtime", "LockOSThread") {
				return true
			}
		}
	}

	return false
}

// isCallToFunction checks if a call expression calls a specific function
func isCallToFunction(call *ast.CallExpr, pkg, funcName string) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		// Direct call like LockOSThread()
		return fun.Name == funcName
	case *ast.SelectorExpr:
		// Qualified call like runtime.LockOSThread()
		if fun.Sel.Name != funcName {
			return false
		}
		if pkg == "" {
			return true // Don't care about package
		}
		if ident, ok := fun.X.(*ast.Ident); ok {
			return ident.Name == pkg
		}
	}
	return false
}
