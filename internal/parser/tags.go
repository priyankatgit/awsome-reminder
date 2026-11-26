package parser

import (
	"regexp"
	"strings"
)

// tagRe matches a whole token that is a hashtag. Tokens such as "#123" or
// "C#" are not tags.
var tagRe = regexp.MustCompile(`^#([A-Za-z][A-Za-z0-9_-]*)$`)

// extractTags removes unquoted #tag tokens, returning the remaining tokens and
// the tags lowercased, deduped, in first-seen order (never nil).
func extractTags(toks []token) ([]token, []string) {
	tags := []string{}
	seen := map[string]bool{}
	rest := make([]token, 0, len(toks))
	for _, t := range toks {
		if !t.Quoted {
			if m := tagRe.FindStringSubmatch(t.Text); m != nil {
				tag := strings.ToLower(m[1])
				if !seen[tag] {
					seen[tag] = true
					tags = append(tags, tag)
				}
				continue
			}
		}
		rest = append(rest, t)
	}
	return rest, tags
}
