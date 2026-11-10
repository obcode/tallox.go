package mail

import (
	"html"
	"regexp"
	"strings"
)

// A deliberately small Markdown: what somebody writing an announcement uses, and nothing that
// could put markup of their own into a mail that goes out under the installation's name.
//
//	blank line        a new paragraph
//	- item, * item    a list
//	1. item           a numbered list
//	**bold**          bold
//	[text](https://…) a link — http, https and mailto only
//	https://…         a link
//
// Everything else is text: escaped first, then these few patterns turned back into tags. There is
// no HTML passthrough, so a body cannot carry a script, a style or a hidden image.

var (
	boldPattern     = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	linkPattern     = regexp.MustCompile(`\[([^\]\n]+)\]\(((?:https?://|mailto:)[^)\s]+)\)`)
	bareURLPattern  = regexp.MustCompile(`(^|[\s(])(https?://[^\s<)]+)`)
	bulletPattern   = regexp.MustCompile(`^\s*[-*]\s+(.*)$`)
	numberedPattern = regexp.MustCompile(`^\s*\d+[.)]\s+(.*)$`)
)

// MarkdownHTML renders a body as HTML, styled inline for mail clients.
func MarkdownHTML(body string) string {
	var b strings.Builder
	for _, block := range blocks(body) {
		lines := strings.Split(block, "\n")
		switch {
		case allMatch(lines, bulletPattern):
			b.WriteString(`<ul style="margin:0 0 16px 0;padding-left:22px;">`)
			for _, l := range lines {
				b.WriteString(`<li style="margin:0 0 6px 0;">` + inline(bulletPattern.FindStringSubmatch(l)[1]) + `</li>`)
			}
			b.WriteString(`</ul>`)
		case allMatch(lines, numberedPattern):
			b.WriteString(`<ol style="margin:0 0 16px 0;padding-left:22px;">`)
			for _, l := range lines {
				b.WriteString(`<li style="margin:0 0 6px 0;">` + inline(numberedPattern.FindStringSubmatch(l)[1]) + `</li>`)
			}
			b.WriteString(`</ol>`)
		default:
			parts := make([]string, 0, len(lines))
			for _, l := range lines {
				parts = append(parts, inline(strings.TrimRight(l, " ")))
			}
			b.WriteString(`<p style="margin:0 0 16px 0;">` + strings.Join(parts, "<br>") + `</p>`)
		}
	}
	return b.String()
}

// blocks splits on blank lines and normalises line ends.
func blocks(body string) []string {
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	var out []string
	for _, block := range regexp.MustCompile(`\n\s*\n`).Split(strings.TrimSpace(body), -1) {
		if block = strings.Trim(block, "\n"); strings.TrimSpace(block) != "" {
			out = append(out, block)
		}
	}
	return out
}

func allMatch(lines []string, p *regexp.Regexp) bool {
	for _, l := range lines {
		if !p.MatchString(l) {
			return false
		}
	}
	return len(lines) > 0
}

// inline escapes a line and turns the few inline patterns back into tags. The patterns run on
// the escaped text, so nothing they produce was ever markup somebody typed.
func inline(line string) string {
	s := html.EscapeString(line)
	s = linkPattern.ReplaceAllStringFunc(s, func(m string) string {
		parts := linkPattern.FindStringSubmatch(m)
		return linkTag(parts[2], parts[1])
	})
	s = bareURLPattern.ReplaceAllStringFunc(s, func(m string) string {
		parts := bareURLPattern.FindStringSubmatch(m)
		// A sentence that ends in an address ends in punctuation that is not part of it.
		address := strings.TrimRight(parts[2], ".,;:!?")
		return parts[1] + linkTag(address, address) + parts[2][len(address):]
	})
	return boldPattern.ReplaceAllString(s, `<strong>$1</strong>`)
}

// linkTag takes an already escaped URL and text. A URL inside an existing link tag is not
// linked twice: bareURLPattern needs whitespace or a bracket before it, and href=" is neither.
func linkTag(escapedURL, escapedText string) string {
	return `<a href="` + escapedURL + `" style="color:#1d4ed8;text-decoration:underline;">` + escapedText + `</a>`
}
