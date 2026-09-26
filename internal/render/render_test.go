package render

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEscape(t *testing.T) {
	if got := Escape(`a<b>&"c"`); got != `a&lt;b&gt;&amp;"c"` {
		t.Fatalf("got %q", got)
	}
}

func TestMarkdownToHTML(t *testing.T) {
	cases := map[string]string{
		"# Title":                        "<b>Title</b>",
		"use `x<y` here":                 "use <code>x&lt;y</code> here",
		"**bold** text":                  "<b>bold</b> text",
		"[docs](https://ex.com/a?b=1&c)": `<a href="https://ex.com/a?b=1&amp;c">docs</a>`,
		"odd ` backtick <":               "odd ` backtick &lt;",
		"```go\nif a < b {}\n```\nafter": "<pre><code>if a &lt; b {}</code></pre>\nafter",
		"```\nunclosed":                  "<pre><code>unclosed</code></pre>",
	}
	for in, want := range cases {
		if got := MarkdownToHTML(in); got != want {
			t.Errorf("MarkdownToHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChunksShortAndEmpty(t *testing.T) {
	if c := Chunks("  \n "); c != nil {
		t.Fatalf("empty text must give nil, got %v", c)
	}
	c := Chunks("hello **world**")
	if len(c) != 1 || c[0] != "hello <b>world</b>" {
		t.Fatalf("chunks: %v", c)
	}
}

func TestChunksLongCodeBlockStaysBalanced(t *testing.T) {
	var b strings.Builder
	b.WriteString("intro\n```\n")
	for i := 0; i < 300; i++ {
		b.WriteString("line of code <number> 1234567890\n")
	}
	b.WriteString("```\noutro")
	chunks := Chunks(b.String())
	if len(chunks) < 2 {
		t.Fatalf("want several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if n := utf8.RuneCountInString(c); n > 4096 {
			t.Fatalf("chunk %d has %d runes", i, n)
		}
		if strings.Count(c, "<pre><code>") != strings.Count(c, "</code></pre>") {
			t.Fatalf("chunk %d has unbalanced code tags", i)
		}
	}
	if !strings.HasPrefix(chunks[1], "<pre><code>") {
		t.Fatalf("second chunk must reopen the code block: %.40q", chunks[1])
	}
	if !strings.HasSuffix(chunks[len(chunks)-1], "outro") {
		t.Fatal("text after the code block is lost")
	}
}

func TestChunksHugeLineOfSpecialChars(t *testing.T) {
	chunks := Chunks(strings.Repeat("<", 5000))
	total := 0
	for i, c := range chunks {
		if n := utf8.RuneCountInString(c); n > 4096 {
			t.Fatalf("chunk %d has %d runes", i, n)
		}
		total += strings.Count(c, "&lt;")
	}
	if total != 5000 {
		t.Fatalf("lost characters: %d of 5000", total)
	}
}
