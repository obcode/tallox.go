package arch

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

// The German this backend sends to people — refusal reasons, validation messages — addresses the
// reader the way the interface does: "Du". A "Sie" in a refusal next to a "Du" on the page reads
// as two different applications, and the next sentence somebody writes copies whichever they saw
// last, so the rule is checked rather than remembered. The same rule, for the same reason, as
// tallox.gui's src/lib/wording.test.ts — the patterns are deliberately the same.
//
// Narrow on purpose: "Sie wird einmal eingetragen" is the pronoun, and "Ihre Leitung" after a
// subject group is its leadership; neither addresses anybody.
var formalAddress = regexp.MustCompile(
	`\b(?i:sind|leiten|können|dürfen|haben|möchten|müssen|lesen|treten) Sie\b` +
		`|\b(?:die|denen|wenn|ob) Sie\b` +
		`|\b(?:mit|bei|von) Ihnen\b` +
		`|\bIhre[nrms]? (?:Studiengang|Studiengänge|Studiengangs|Fachgruppe|Fachgruppen|` +
		`Studiengangsleitung|Fachgruppenleitung)\b`)

// TestUserFacingGermanSaysDu reads every string literal in the non-test, non-generated Go source
// and refuses the formal address. String literals only: a comment quoting how the faculty
// talks is not a message anybody receives.
func TestUserFacingGermanSaysDu(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	checked := 0
	err := filepath.WalkDir("../..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "generated", "node_modules", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			strings.HasSuffix(path, ".generated.go") || strings.HasSuffix(path, "_gen.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		checked++
		// Concatenated literals ("…, die Sie " + "leiten …") are joined before matching, so a
		// sentence broken across lines is read as the sentence it is.
		ast.Inspect(file, func(n ast.Node) bool {
			text, ok := literalText(n)
			if !ok {
				return true
			}
			if m := formalAddress.FindString(text); m != "" {
				t.Errorf("%s: %q addresses the reader formally (%q); the interface says \"Du\"",
					fset.Position(n.Pos()), text, m)
			}
			// A joined expression has been read whole; do not report its halves again.
			_, joined := n.(*ast.BinaryExpr)
			return !joined
		})
		return nil
	})
	if err != nil {
		t.Fatalf("cannot read the source: %v", err)
	}
	if checked == 0 {
		t.Fatal("no Go file was read — this test checked nothing")
	}
}

// literalText is the text of a string literal, or of a chain of them joined with +.
func literalText(n ast.Node) (string, bool) {
	switch e := n.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := literalText(e.X)
		if !ok {
			return "", false
		}
		right, ok := literalText(e.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	default:
		return "", false
	}
}
