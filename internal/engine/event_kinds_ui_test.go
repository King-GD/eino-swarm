package engine

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	swarm "github.com/LubyRuffy/eino-swarm"
)

// storedEventKinds lists every event kind the engine and store write: Kind*
// string constants plus literal `Kind: "..."` fields in actual Event values.
func storedEventKinds(t *testing.T) map[string]string {
	t.Helper()
	kinds := map[string]string{}
	fset := token.NewFileSet()
	for _, dir := range []string{".", filepath.Join("..", "store")} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for kind, origin := range eventKindsInFile(fset, file) {
				kinds[kind] = origin
			}
		}
	}
	for k := swarm.NotifyKind(0); k.String() != "unknown"; k++ {
		kinds[k.String()] = "swarm." + k.String()
	}
	return kinds
}

// eventKindsInFile recognises persisted Event literals, including elided types
// in slices/maps. A field named Kind in a panel protocol or another package's
// Event is not evidence that it enters the chat transcript.
func eventKindsInFile(fset *token.FileSet, file *ast.File) map[string]string {
	kinds := map[string]string{}
	storeImports := map[string]bool{}
	localEvent := file.Name.Name == "store"
	aliases := map[string]ast.Expr{}
	for _, imp := range file.Imports {
		name, err := strconv.Unquote(imp.Path.Value)
		if err != nil || name != "github.com/LubyRuffy/eino-swarm/internal/store" {
			continue
		}
		alias := "store"
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		if alias == "." {
			localEvent = true
		} else {
			storeImports[alias] = true
		}
	}
	for _, decl := range file.Decls {
		group, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range group.Specs {
			if alias, ok := spec.(*ast.TypeSpec); ok && alias.Assign.IsValid() {
				aliases[alias.Name.Name] = alias.Type
			}
		}
	}
	var eventType func(ast.Expr, map[string]bool) bool
	eventType = func(expr ast.Expr, seen map[string]bool) bool {
		switch typ := expr.(type) {
		case *ast.Ident:
			if typ.Name == "Event" && localEvent {
				return true
			}
			if seen[typ.Name] {
				return false
			}
			seen[typ.Name] = true
			return eventType(aliases[typ.Name], seen)
		case *ast.SelectorExpr:
			pkg, ok := typ.X.(*ast.Ident)
			return ok && storeImports[pkg.Name] && typ.Sel.Name == "Event"
		case *ast.StarExpr:
			return eventType(typ.X, seen)
		}
		return false
	}
	stack := []ast.Node{}
	var compositeType func(int) ast.Expr
	compositeType = func(index int) ast.Expr {
		literal := stack[index].(*ast.CompositeLit)
		if literal.Type != nil {
			return literal.Type
		}
		for i := index - 1; i >= 0; i-- {
			if _, ok := stack[i].(*ast.CompositeLit); !ok {
				continue
			}
			switch outer := compositeType(i).(type) {
			case *ast.ArrayType:
				return outer.Elt
			case *ast.MapType:
				if i+1 < len(stack) {
					if pair, ok := stack[i+1].(*ast.KeyValueExpr); ok && pair.Key == literal {
						return outer.Key
					}
				}
				return outer.Value
			}
			return nil
		}
		return nil
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, node)
		var name string
		var value ast.Expr
		switch n := node.(type) {
		case *ast.ValueSpec:
			if len(n.Names) != 1 || len(n.Values) != 1 || !strings.HasPrefix(n.Names[0].Name, "Kind") {
				return true
			}
			name, value = n.Names[0].Name, n.Values[0]
		case *ast.KeyValueExpr:
			key, ok := n.Key.(*ast.Ident)
			if !ok || key.Name != "Kind" {
				return true
			}
			typedEvent := false
			for i := len(stack) - 2; i >= 0; i-- {
				if _, ok := stack[i].(*ast.CompositeLit); ok {
					typedEvent = eventType(compositeType(i), map[string]bool{})
					break
				}
			}
			if !typedEvent {
				return true
			}
			name, value = "Event.Kind field", n.Value
		default:
			return true
		}
		literal, ok := value.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		kind, err := strconv.Unquote(literal.Value)
		if err == nil && kind != "" {
			kinds[kind] = fset.Position(literal.Pos()).String() + " " + name
		}
		return true
	})
	return kinds
}

// The desktop reducer shows a kind it does not recognise as a chat card of
// the event's raw text. A new internal receipt without an explicit decision
// there put raw query_history SQL into a conversation; every stored kind must
// be named by the reducer, either as a chat row or as Trace-only metadata.
func TestEveryStoredEventKindHasAnExplicitDesktopTranscriptDecision(t *testing.T) {
	kinds := storedEventKinds(t)
	if _, ok := kinds[KindHistoryReadStarted]; !ok {
		t.Fatalf("kind scan missed %s: %v", KindHistoryReadStarted, kinds)
	}
	if _, ok := kinds["history_registry_inherited"]; !ok {
		t.Fatal("kind scan missed store-written event literals")
	}
	sources, err := filepath.Glob(filepath.Join("..", "..", "frontend", "src", "lib", "transcript*.ts"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("transcript reducer sources: %v %v", sources, err)
	}
	var reducer strings.Builder
	for _, path := range sources {
		if strings.HasSuffix(path, ".test.ts") {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		reducer.Write(raw)
	}
	for kind, origin := range kinds {
		if !strings.Contains(reducer.String(), strconv.Quote(kind)) {
			t.Errorf("event kind %q (%s) has no explicit desktop transcript decision", kind, origin)
		}
	}
}
