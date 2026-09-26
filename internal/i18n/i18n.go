// Package i18n holds every user-facing string of the bot and of the tgsync
// command line in English and Russian. The language is chosen once at
// startup from BOT_LANGUAGE; code asks for text by key with T or N.
package i18n

import (
	"fmt"
	"strings"
	"sync/atomic"
)

// Lang is an interface language.
type Lang string

const (
	EN Lang = "en"
	RU Lang = "ru"
)

// Default is the language used when BOT_LANGUAGE is not set.
const Default = EN

// Parse maps a BOT_LANGUAGE value to a language; ok is false for anything
// other than en or ru.
func Parse(s string) (Lang, bool) {
	switch Lang(strings.ToLower(strings.TrimSpace(s))) {
	case EN:
		return EN, true
	case RU:
		return RU, true
	}
	return "", false
}

// Text is one catalog entry in both languages. Plural entries (keys with a
// ".n." segment, used with N) list forms separated by "|": "one|other" in
// English and "one|few|many" in Russian.
type Text struct{ EN, RU string }

var (
	current atomic.Value // Lang
	catalog = map[string]Text{}
)

func init() { current.Store(Default) }

// Set switches the interface language. main calls it once before anything
// sends text; tests call it to pin a language.
func Set(l Lang) { current.Store(l) }

// Current returns the interface language.
func Current() Lang { return current.Load().(Lang) }

// register adds entries to the catalog; every catalog file calls it from init.
func register(m map[string]Text) {
	for k, v := range m {
		if _, dup := catalog[k]; dup {
			panic("i18n: duplicate key " + k)
		}
		catalog[k] = v
	}
}

func lookup(key string) string {
	t, ok := catalog[key]
	if !ok {
		return key
	}
	if Current() == RU {
		return t.RU
	}
	return t.EN
}

// T returns the text for key in the current language, formatted with args
// when any are given. An unknown key comes back as is, so a typo shows up in
// the chat instead of an empty message.
func T(key string, args ...any) string {
	s := lookup(key)
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

// Any reports whether s equals the text of key in any language. It matches
// values stored before BOT_LANGUAGE changed, such as a default topic title.
func Any(key, s string) bool {
	t, ok := catalog[key]
	return ok && (s == t.EN || s == t.RU)
}

// N picks the plural form of key for n and formats it with args. n is not
// passed to Sprintf implicitly: include it in args when the text shows it.
func N(key string, n int, args ...any) string {
	forms := strings.Split(lookup(key), "|")
	s := forms[pluralIndex(Current(), n, len(forms))]
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

func pluralIndex(l Lang, n, forms int) int {
	if n < 0 {
		n = -n
	}
	i := 1
	if l == RU {
		switch {
		case n%10 == 1 && n%100 != 11:
			i = 0
		case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
			i = 1
		default:
			i = 2
		}
	} else if n == 1 {
		i = 0
	}
	if i >= forms {
		i = forms - 1
	}
	return i
}
