package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// dynamicPrefixes are keys the code builds at run time as prefix + suffix;
// each lists the suffixes the code can produce, so the catalog must have
// them all and they count as used.
var dynamicPrefixes = map[string][]string{
	"render.weekday.": {"0", "1", "2", "3", "4", "5", "6"},                                         // render/usage.go: time.Weekday
	"render.window.":  {"five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet", "overage"}, // render/usage.go: knownWindows
	"session.mode.":   {"default", "acceptEdits", "plan"},                                          // session/manager.go: modeHelp
}

// errorTypes are string types whose Error method looks the value up with T,
// so a conversion like localError("tg.topic_gone") names a key.
var errorTypes = map[string]bool{"localError": true, "keyedError": true}

// keyLike matches literals that look like catalog keys with at least three
// segments ("render.ctx.cat.messages"), such as map values holding keys.
// Two-segment literals are left out: "profiles.yaml" is a file name.
var keyLike = regexp.MustCompile(`^[a-z]+(\.[A-Za-z0-9_]+){2,}$`)

type use struct {
	key  string
	pos  token.Position
	fn   string // T, N, Any or "" for a key held elsewhere
	args int    // format arguments passed, -1 when unknown (args...)
}

// scanSources parses the non-test Go files under internal and cmd and returns
// every catalog key they reference, plus all string literals seen.
func scanSources(t *testing.T) (uses []use, literals map[string]bool) {
	t.Helper()
	literals = map[string]bool{}
	fset := token.NewFileSet()
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if filepath.Base(filepath.Dir(path)) == "i18n" {
				return nil // the catalog itself
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			formats := map[*ast.CallExpr]int{} // i18n.T calls used as a format, with the outer argument count
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BasicLit:
					if s, ok := stringLit(n); ok {
						literals[s] = true
						if keyLike.MatchString(s) && hasNamespace(s) {
							uses = append(uses, use{key: s, pos: fset.Position(n.Pos()), args: -1})
						}
					}
				case *ast.CallExpr:
					if len(n.Args) == 0 {
						return true
					}
					if id, ok := n.Fun.(*ast.Ident); ok && errorTypes[id.Name] {
						if s, ok := stringLit(n.Args[0]); ok {
							uses = append(uses, use{key: s, pos: fset.Position(n.Pos()), args: 0})
						}
						return true
					}
					sel, ok := n.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					x, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					if x.Name == "fmt" && sel.Sel.Name == "Errorf" {
						// fmt.Errorf(i18n.T("key"), args...): the text is the
						// format, so it consumes the Errorf arguments (%w).
						if inner, ok := n.Args[0].(*ast.CallExpr); ok && len(n.Args) > 1 && !n.Ellipsis.IsValid() {
							formats[inner] = len(n.Args) - 1
						}
						return true
					}
					if x.Name == "slog" {
						// Logs are for the operator and stay in English.
						for _, a := range n.Args {
							ast.Inspect(a, func(m ast.Node) bool {
								if s, ok := m.(*ast.SelectorExpr); ok {
									if id, ok := s.X.(*ast.Ident); ok && id.Name == "i18n" {
										t.Errorf("%s: slog message uses i18n; keep logs in English", fset.Position(m.Pos()))
									}
								}
								return true
							})
						}
						return true
					}
					if x.Name != "i18n" {
						return true
					}
					fn := sel.Sel.Name
					if fn != "T" && fn != "N" && fn != "Any" {
						return true
					}
					s, ok := stringLit(n.Args[0])
					if !ok {
						if prefix, ok := prefixLit(n.Args[0]); ok {
							if _, known := dynamicPrefixes[prefix]; !known {
								t.Errorf("%s: key prefix %q is built at run time; list it in dynamicPrefixes", fset.Position(n.Pos()), prefix)
							}
						}
						return true // a key held in a variable: covered by the literal scan
					}
					args := -1
					if !n.Ellipsis.IsValid() {
						switch fn {
						case "T":
							args = len(n.Args) - 1
							if outer, ok := formats[n]; ok && args == 0 {
								args = outer
							}
						case "N":
							args = len(n.Args) - 2
						}
					}
					uses = append(uses, use{key: s, pos: fset.Position(n.Pos()), fn: fn, args: args})
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return uses, literals
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// prefixLit returns "p." for an argument written as "p." + something.
func prefixLit(e ast.Expr) (string, bool) {
	b, ok := e.(*ast.BinaryExpr)
	if !ok || b.Op != token.ADD {
		return "", false
	}
	return stringLit(b.X)
}

func hasNamespace(s string) bool {
	ns, _, _ := strings.Cut(s, ".")
	for k := range catalog {
		if strings.HasPrefix(k, ns+".") {
			return true
		}
	}
	return false
}

// countVerbs is the number of arguments a format consumes.
func countVerbs(s string) int {
	n := 0
	for _, v := range verb.FindAllString(s, -1) {
		if v != "%%" {
			n++
		}
	}
	return n
}

// TestKeysInCode fails when code asks for a key the catalog lacks, calls N
// with a non-plural key or T with a plural one, or passes a different number
// of format arguments than the text has verbs.
func TestKeysInCode(t *testing.T) {
	uses, _ := scanSources(t)
	if len(uses) < 100 {
		t.Fatalf("found only %d key uses; is the source scan broken?", len(uses))
	}
	for _, u := range uses {
		txt, ok := catalog[u.key]
		if !ok {
			t.Errorf("%s: unknown key %q", u.pos, u.key)
			continue
		}
		plural := strings.Contains(u.key, ".n.")
		switch {
		case u.fn == "N" && !plural:
			t.Errorf("%s: N with key %q that has no .n. segment", u.pos, u.key)
		case (u.fn == "T" || u.fn == "Any") && plural:
			t.Errorf("%s: %s with plural key %q, use N", u.pos, u.fn, u.key)
		}
		if u.args < 0 {
			continue
		}
		want := countVerbs(txt.EN)
		if plural {
			want = countVerbs(strings.Split(txt.EN, "|")[0])
		}
		// A key without arguments comes back verbatim, so its text must not
		// contain verbs either, not even %%: T would print them raw.
		if u.args == 0 && strings.Contains(txt.EN+txt.RU, "%%") {
			t.Errorf("%s: %q has %%%% but is used without arguments", u.pos, u.key)
		}
		if u.args != want {
			t.Errorf("%s: %q takes %d arguments, got %d", u.pos, u.key, want, u.args)
		}
	}
}

// TestNoUnusedKeys fails when the catalog holds a key no code refers to.
func TestNoUnusedKeys(t *testing.T) {
	_, literals := scanSources(t)
	for prefix, suffixes := range dynamicPrefixes {
		for _, s := range suffixes {
			if _, ok := catalog[prefix+s]; !ok {
				t.Errorf("dynamic key %q is missing", prefix+s)
			}
			literals[prefix+s] = true
		}
	}
	var unused []string
	for k := range catalog {
		if !strings.HasPrefix(k, "test.") && !literals[k] {
			unused = append(unused, k)
		}
	}
	slices.Sort(unused)
	for _, k := range unused {
		t.Errorf("unused key %q", k)
	}
}
