// Package render turns agent output into Telegram HTML.
package render

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// Escape makes plain text safe for Telegram HTML.
func Escape(s string) string { return htmlEscaper.Replace(s) }

var (
	headRe = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	boldRe = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	linkRe = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s"]+)\)`)
)

// MarkdownToHTML converts the Markdown subset Claude usually writes:
// fenced code, inline code, headings, bold and links. Everything else is escaped.
func MarkdownToHTML(md string) string {
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines))
	inCode := false
	var code []string
	flushCode := func() {
		out = append(out, "<pre><code>"+Escape(strings.Join(code, "\n"))+"</code></pre>")
		code = nil
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCode {
				flushCode()
			}
			inCode = !inCode
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		if m := headRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			out = append(out, "<b>"+inline(m[1])+"</b>")
			continue
		}
		out = append(out, inline(line))
	}
	if inCode {
		flushCode()
	}
	return strings.Join(out, "\n")
}

func inline(s string) string {
	if strings.Count(s, "`")%2 != 0 {
		return decorate(Escape(s))
	}
	parts := strings.Split(s, "`")
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 {
			b.WriteString("<code>" + Escape(p) + "</code>")
		} else {
			b.WriteString(decorate(Escape(p)))
		}
	}
	return b.String()
}

func decorate(escaped string) string {
	escaped = boldRe.ReplaceAllString(escaped, "<b>$1</b>")
	return linkRe.ReplaceAllString(escaped, `<a href="$2">$1</a>`)
}

const (
	maxHTML = 4000 // limit for a chunk made of several lines
	maxLine = 800  // 800 runes escape to at most 4000 ("&" → "&amp;")
)

// Chunks splits Markdown into Telegram-sized HTML messages.
// A code block cut by a chunk border is closed and reopened in the next chunk.
func Chunks(md string) []string {
	if strings.TrimSpace(md) == "" {
		return nil
	}
	var chunks, cur []string
	inCode := false
	for _, line := range explode(strings.Split(md, "\n")) {
		next := append(append([]string(nil), cur...), line)
		onlyReopenedFence := len(cur) == 1 && inCode && cur[0] == "```"
		if len(cur) > 0 && !onlyReopenedFence && utf8.RuneCountInString(MarkdownToHTML(strings.Join(next, "\n"))) > maxHTML {
			chunks = append(chunks, MarkdownToHTML(strings.Join(cur, "\n")))
			cur = nil
			if inCode {
				cur = []string{"```"}
			}
			next = append(append([]string(nil), cur...), line)
		}
		cur = next
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
		}
	}
	if len(cur) > 0 {
		chunks = append(chunks, MarkdownToHTML(strings.Join(cur, "\n")))
	}
	return chunks
}

func explode(lines []string) []string {
	var out []string
	for _, l := range lines {
		r := []rune(l)
		for len(r) > maxLine {
			out = append(out, string(r[:maxLine]))
			r = r[maxLine:]
		}
		out = append(out, string(r))
	}
	return out
}

// oneLine joins lines and cuts s to max runes.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
