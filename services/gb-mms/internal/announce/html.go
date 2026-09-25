package announce

import (
	"html"
	"regexp"
	"strings"
)

var (
	// An opening <li> is the bullet; the closing block tags and <br> are the
	// line breaks. Nothing else in the markup survives.
	bulletTag = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	breakTag  = regexp.MustCompile(`(?i)<(?:br\s*/?|/p|/div|/li|/h[1-6]|/tr)\s*>`)
	anyTag    = regexp.MustCompile(`<[^>]*>`)
)

// plain turns a rendered HTML fragment into text. GameBanana serves changelog
// bodies as markup, which Discord shows verbatim.
func plain(s string) string {
	s = bulletTag.ReplaceAllString(s, "\n• ")
	s = breakTag.ReplaceAllString(s, "\n")
	s = anyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	lines := make([]string, 0, strings.Count(s, "\n")+1)

	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}

	return strings.Join(lines, "\n")
}

// inline is plain for a field that has to stay on one line: a bullet, a label,
// the description blurb.
func inline(s string) string {
	return strings.Join(strings.Fields(plain(s)), " ")
}
