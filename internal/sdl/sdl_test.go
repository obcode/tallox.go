package sdl_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/obcode/tallox.go/internal/sdl"
)

func committed(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", sdl.FileName))
	if err != nil {
		t.Fatalf("reading the committed schema: %v", err)
	}
	return data
}

// TestCommittedSchemaIsTheServedSchema keeps the contract honest: the committed file is the
// served schema, rendered, byte for byte. The `generate` CI job notices a stale file as well;
// this test says so on every `go test`, including locally, before anything is pushed.
func TestCommittedSchemaIsTheServedSchema(t *testing.T) {
	t.Parallel()

	if !bytes.Equal(committed(t), sdl.Render(sdl.Served())) {
		t.Fatalf("%s does not match the served schema — run `go generate ./...` and commit it",
			sdl.FileName)
	}
}

// TestSchemaFileStandsOnItsOwn is what a client does with the file: load it without a server.
// Rendering what was loaded has to give the same bytes again — otherwise the file would lose
// something on the way, and a client would build against less than the server serves.
func TestSchemaFileStandsOnItsOwn(t *testing.T) {
	t.Parallel()

	data := committed(t)
	loaded, err := gqlparser.LoadSchema(&ast.Source{Name: sdl.FileName, Input: string(data)})
	if err != nil {
		t.Fatalf("%s does not load as a schema: %v", sdl.FileName, err)
	}
	if !bytes.Equal(data, sdl.Render(loaded)) {
		t.Fatalf("%s does not survive a round trip through the parser", sdl.FileName)
	}
}

// TestSchemaFileCarriesTheDoorDirectives is the reason the file exists: introspection declares
// the directives and never says where they are used. Every usage on the served schema has to be
// in the file, on the same field.
func TestSchemaFileCarriesTheDoorDirectives(t *testing.T) {
	t.Parallel()

	loaded, err := gqlparser.LoadSchema(&ast.Source{Name: sdl.FileName, Input: string(committed(t))})
	if err != nil {
		t.Fatalf("%s does not load as a schema: %v", sdl.FileName, err)
	}

	seen := map[string]int{}
	for name, def := range sdl.Served().Types {
		if def.BuiltIn {
			continue
		}
		for _, field := range def.Fields {
			for _, directive := range field.Directives {
				seen[directive.Name]++
				in := loaded.Types[name].Fields.ForName(field.Name)
				if in == nil || in.Directives.ForName(directive.Name) == nil {
					t.Errorf("%s.%s carries @%s on the server but not in %s",
						name, field.Name, directive.Name, sdl.FileName)
				}
			}
		}
	}

	for _, name := range []string{"scope", "interactiveOnly", "fullReach"} {
		if seen[name] == 0 {
			t.Errorf("no field carries @%s — has the directive been renamed?", name)
		}
	}
}
