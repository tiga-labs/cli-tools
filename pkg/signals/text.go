package signals

import (
	"html"
	"regexp"
	"strings"
)

// valueTags are the formatting tags gandalf's JSON prompt asks for (span, b, i, em) plus br.
var (
	valueTags  = regexp.MustCompile(`(?i)</?(span|b|i|em)(\s[^<>]*)?>`)
	valueBreak = regexp.MustCompile(`(?i)<br\s*/?>`)
	// entity matches only ;-terminated entities: html.UnescapeString alone also decodes
	// HTML4 names without a semicolon, turning "Brand&copywriting" into "Brand©writing".
	entity = regexp.MustCompile(`&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);`)
)

// stripValueHTML turns a JSON-path answer into plain text: gandalf's formatting tags are
// removed, <br> becomes a newline, and entities are unescaped. Other text is left alone.
func stripValueHTML(s string) string {
	if !strings.Contains(s, "<") && !strings.Contains(s, "&") {
		return strings.TrimSpace(s)
	}
	s = valueBreak.ReplaceAllString(s, "\n")
	s = valueTags.ReplaceAllString(s, "")
	s = entity.ReplaceAllStringFunc(s, html.UnescapeString)
	return strings.TrimSpace(s)
}
