package arch

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Comments are English without exception; German is only what a person is shown — the
// refusal reasons, the validation messages. The rule was said more than once before it was
// checked, and a file with German comments is the one the next comment copies, so it is
// checked here rather than remembered. tallox.gui's src/lib/commentLanguage.ts applies the
// same heuristic to the interface; keep the two word lists in step.
//
// A heuristic over function words, not over vocabulary: an English comment quoting
// „wenn es brennt, kann es niemand“ or naming the Fachgruppe is correct, and a German sentence
// without a single umlaut is not.

var germanWords = wordSet(`der die das den dem des und oder nicht wird werden wurde wäre würde
	ist sind ein eine einen einem einer eines für mit auf bei nur auch noch wenn weil dass sich
	sie er wir ich hier dort damit sonst aber denn doch schon kein keine keinen nach über unter
	zum zur vom im ins beim als wie wo wer welche welcher dieser diese dieses jede jeder alle
	kann muss soll darf hat haben steht stehen gibt geht nichts etwas immer nie jetzt dann ob
	bis seit ohne durch aus zu eben bleibt gerade nämlich deshalb weshalb trotzdem bereits
	fuer ueber koennen muessen laesst waere wuerde haelt gehoert naechste`)

var englishWords = wordSet(`the a an and or not is are be been to of in on for with that this
	these those it its if when because only also as by from at which who can must should would
	could does do has have was were but so than then there what into here why how one no every
	each where whether after before rather instead`)

func wordSet(list string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(list) {
		set[w] = true
	}
	return set
}

var (
	// Quoted text is what the interface says, or what somebody said — not the comment's language.
	quoted = regexp.MustCompile("„[^“”]*[“”]|\"[^\"\\n]*\"|`[^`]*`|«[^»]*»")
	// One sentence at a time, so a comment that starts in English and ends in German is caught.
	sentenceBreak = regexp.MustCompile(`[.!?:;]\s+|\s+[—–]\s+`)
	letters       = regexp.MustCompile(`\p{L}+`)
)

func germanSentences(comment string) []string {
	var found []string
	// Whitespace first: a quotation wrapped across two comment lines is still one quotation.
	text := quoted.ReplaceAllString(strings.Join(strings.Fields(comment), " "), " ")
	for _, sentence := range sentenceBreak.Split(text, -1) {
		if isGerman(sentence) {
			found = append(found, strings.TrimSpace(sentence))
		}
	}
	return found
}

func isGerman(sentence string) bool {
	var german, english int
	for _, w := range letters.FindAllString(strings.ToLower(sentence), -1) {
		if germanWords[w] {
			german++
		}
		if englishWords[w] {
			english++
		}
	}
	return german >= 2 && german > english
}

type sourceComment struct {
	pos  string
	text string
}

// generated is the Go convention for a file nobody edits by hand: sqlc and gqlgen both write it.
var generated = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

func goComments(fset *token.FileSet, path string) ([]sourceComment, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if generated.Match(src) {
		return nil, nil
	}
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var found []sourceComment
	for _, group := range file.Comments {
		found = append(found, sourceComment{fset.Position(group.Pos()).String(), group.Text()})
	}
	return found, nil
}

var (
	// A run of line comments is one comment, the way go/ast groups them.
	sqlComment     = regexp.MustCompile(`(?:--[^\n]*(?:\n[ \t]*)?)+|/\*[\s\S]*?\*/`)
	graphqlComment = regexp.MustCompile(`"""[\s\S]*?"""|(?:#[^\n]*(?:\n[ \t]*)?)+`)
)

// textComments reads comments by pattern. Good enough for migrations, queries and schema files,
// where a `--` or `#` inside a string literal is something nobody has written.
func textComments(path string, pattern *regexp.Regexp) ([]sourceComment, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var found []sourceComment
	for _, loc := range pattern.FindAllIndex(src, -1) {
		line := 1 + strings.Count(string(src[:loc[0]]), "\n")
		text := strings.NewReplacer("--", " ", "#", " ", `"""`, " ", "/*", " ", "*/", " ").
			Replace(string(src[loc[0]:loc[1]]))
		found = append(found, sourceComment{path + ":" + strconv.Itoa(line), text})
	}
	return found, nil
}

// TestCommentsAreEnglish reads every comment in the hand-written Go, SQL and GraphQL source —
// tests included, since a test is read as often as the code it checks.
func TestCommentsAreEnglish(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	checked := map[string]int{}
	err := filepath.WalkDir("../..", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "generated", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		var comments []sourceComment
		switch filepath.Ext(path) {
		case ".go":
			comments, err = goComments(fset, path)
		case ".sql":
			comments, err = textComments(path, sqlComment)
		case ".graphqls":
			comments, err = textComments(path, graphqlComment)
		default:
			return nil
		}
		if err != nil {
			return err
		}
		checked[filepath.Ext(path)]++
		for _, c := range comments {
			for _, s := range germanSentences(c.text) {
				t.Errorf("%s: German comment: %s", c.pos, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot read the source: %v", err)
	}
	for _, ext := range []string{".go", ".sql", ".graphqls"} {
		if checked[ext] == 0 {
			t.Errorf("no %s file was read — this test checked nothing there", ext)
		}
	}
}

func TestGermanSentences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text   string
		german bool
	}{
		// A comment that switches language halfway through.
		{"Named for what a token actually reaches here: über ein Token sieht auch eine Leitung nur", true},
		{"Der Zustand des Speicherns steht über der Seite, nicht in ihr.", true},
		{"das ist fuer den Fall, dass niemand da ist", true},
		// Quoting the interface, or the faculty, is not writing German.
		{"The rule is „wenn es brennt, kann es niemand“ and nothing more.", false},
		{`A module split such as "SU mit Übung" is entered once per module.`, false},
		{"Keeps the Fachgruppe in the URL so the tab survives a reload.", false},
		{"die", false},
	}
	for _, c := range cases {
		if got := len(germanSentences(c.text)) > 0; got != c.german {
			t.Errorf("germanSentences(%q) found German = %v, want %v", c.text, got, c.german)
		}
	}
}

// The walk has to see comments, and only comments: the refusal texts in the same files are
// German on purpose.
func TestGoCommentsSkipStrings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	src := "package x\n\n// Der Zähler steht oben, nicht in der Liste.\nconst label = \"Das ist nicht der Kommentar und bleibt deutsch\"\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	comments, err := goComments(token.NewFileSet(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 1 || len(germanSentences(comments[0].text)) == 0 {
		t.Errorf("want exactly the one German comment, got %+v", comments)
	}

	gen := filepath.Join(dir, "gen.go")
	if err := os.WriteFile(gen, []byte("// Code generated by sqlc. DO NOT EDIT.\n\npackage x\n\n// Das ist generiert und nicht unsere Sache.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if comments, _ := goComments(token.NewFileSet(), gen); len(comments) != 0 {
		t.Errorf("generated files are skipped, got %+v", comments)
	}
}

// A run of `--` lines is read as one comment: otherwise a quotation wrapped across two lines
// loses its closing mark and the German inside it is counted as the comment's own.
func TestSQLCommentsAreReadAsBlocks(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "x.sql")
	src := "-- The faculty asked for \"ich mache die Vorlesung,\n-- das Praktikum macht jemand anders\" in one line.\nSELECT 'nicht ein Kommentar, und das ist gut so';\n-- Das ist ein deutscher Kommentar, und der fällt auf.\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	comments, err := textComments(path, sqlComment)
	if err != nil {
		t.Fatal(err)
	}
	var german []string
	for _, c := range comments {
		german = append(german, germanSentences(c.text)...)
	}
	if len(comments) != 2 || len(german) != 1 {
		t.Errorf("want two comments and one German sentence, got %+v / %q", comments, german)
	}
}
