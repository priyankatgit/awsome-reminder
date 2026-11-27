package parser

import (
	"strings"

	"awesome-reminder/internal/model"
)

// priorityTokens maps a whole unquoted token to its priority. A bare "!" or
// "!!" is plain text, as is anything like "!important" or "wow!!".
var priorityTokens = map[string]string{
	"!high":   model.PriorityHigh,
	"!h":      model.PriorityHigh,
	"!!!":     model.PriorityHigh,
	"!medium": model.PriorityMedium,
	"!med":    model.PriorityMedium,
	"!m":      model.PriorityMedium,
	"!low":    model.PriorityLow,
	"!l":      model.PriorityLow,
}

// extractPriority removes every unquoted priority token and returns the rest
// plus the priority of the first one found ("" when none).
func extractPriority(toks []token) ([]token, string) {
	prio := model.PriorityNone
	rest := make([]token, 0, len(toks))
	for _, t := range toks {
		if !t.Quoted {
			if p, ok := priorityTokens[strings.ToLower(t.Text)]; ok {
				if prio == model.PriorityNone {
					prio = p
				}
				continue
			}
		}
		rest = append(rest, t)
	}
	return rest, prio
}

// renderPriority is the token Render emits for a priority.
func renderPriority(p string) string {
	switch p {
	case model.PriorityHigh:
		return "!high"
	case model.PriorityMedium:
		return "!med"
	case model.PriorityLow:
		return "!low"
	}
	return ""
}
