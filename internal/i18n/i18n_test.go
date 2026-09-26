package i18n

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var verb = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[a-zA-Z%]`)

func verbs(s string) []string {
	v := verb.FindAllString(s, -1)
	slices.Sort(v)
	return v
}

// TestCatalog checks that every entry has both languages, the same format
// verbs in both, and the right number of plural forms.
func TestCatalog(t *testing.T) {
	if len(catalog) == 0 {
		t.Fatal("catalog is empty")
	}
	for k, v := range catalog {
		if strings.HasPrefix(k, "test.") {
			continue
		}
		if strings.TrimSpace(v.EN) == "" || strings.TrimSpace(v.RU) == "" {
			t.Errorf("%s: missing a language", k)
			continue
		}
		if strings.Contains(k, ".n.") {
			enForms, ruForms := strings.Split(v.EN, "|"), strings.Split(v.RU, "|")
			if len(enForms) != 2 || len(ruForms) != 3 {
				t.Errorf("%s: plural needs 2 English and 3 Russian forms, got %d and %d", k, len(enForms), len(ruForms))
				continue
			}
			for _, f := range append(enForms[1:], ruForms...) {
				if !slices.Equal(verbs(f), verbs(enForms[0])) {
					t.Errorf("%s: form %q has verbs %v, want %v", k, f, verbs(f), verbs(enForms[0]))
				}
			}
			continue
		}
		if !slices.Equal(verbs(v.EN), verbs(v.RU)) {
			t.Errorf("%s: verbs differ: en %v, ru %v", k, verbs(v.EN), verbs(v.RU))
		}
	}
}

func TestParse(t *testing.T) {
	for in, want := range map[string]Lang{"en": EN, "RU": RU, " ru ": RU} {
		if got, ok := Parse(in); !ok || got != want {
			t.Errorf("Parse(%q) = %q, %v", in, got, ok)
		}
	}
	if _, ok := Parse("de"); ok {
		t.Error("Parse(de) accepted")
	}
}

func TestPluralIndex(t *testing.T) {
	ru := map[int]int{0: 2, 1: 0, 2: 1, 4: 1, 5: 2, 11: 2, 12: 2, 21: 0, 22: 1, 111: 2, 101: 0}
	for n, want := range ru {
		if got := pluralIndex(RU, n, 3); got != want {
			t.Errorf("ru %d: got %d, want %d", n, got, want)
		}
	}
	en := map[int]int{0: 1, 1: 0, 2: 1, 21: 1}
	for n, want := range en {
		if got := pluralIndex(EN, n, 2); got != want {
			t.Errorf("en %d: got %d, want %d", n, got, want)
		}
	}
}

func TestTAndN(t *testing.T) {
	register(map[string]Text{
		"test.hello":  {EN: "Hello, %s", RU: "Привет, %s"},
		"test.n.file": {EN: "%d file|%d files", RU: "%d файл|%d файла|%d файлов"},
	})
	defer Set(Current())
	Set(RU)
	if got := T("test.hello", "мир"); got != "Привет, мир" {
		t.Errorf("T = %q", got)
	}
	if got := N("test.n.file", 3, 3); got != "3 файла" {
		t.Errorf("N = %q", got)
	}
	Set(EN)
	if got := N("test.n.file", 1, 1); got != "1 file" {
		t.Errorf("N = %q", got)
	}
	if !Any("test.hello", "Привет, %s") || Any("test.hello", "Hi") {
		t.Error("Any does not match both languages")
	}
	if got := T("test.missing"); got != "test.missing" {
		t.Errorf("missing key = %q", got)
	}
}
