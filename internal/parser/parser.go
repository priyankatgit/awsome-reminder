// Package parser turns quick-capture text into item text, tags and a due time.
package parser

import (
	"strings"
	"time"
)

// Clock is a wall-clock time of day.
type Clock struct{ Hour, Minute int }

// Config holds the user settings the parser depends on.
type Config struct {
	Loc          *time.Location
	DefaultTime  Clock        // time used when a date has no time (09:00)
	NextWeekDay  time.Weekday // weekday meant by "next week" (Monday)
	NextWeekTime Clock        // time used for "next week" (09:00)
}

// Result is the outcome of parsing one input line.
// The kind is derived by callers: DueAt != nil means reminder.
type Result struct {
	Text     string
	Tags     []string   // lowercase, deduped, first-seen order; never nil
	DueAt    *time.Time // UTC
	RRule    string
	Warnings []string // e.g. "time is in the past"
}

// token is a whitespace-delimited word of the input. Quoted is true when the
// token came from a double-quoted segment; such tokens are literal text and
// must never be interpreted (as prefixes, tags or time phrases).
type token struct {
	Text   string
	Quoted bool
}

// prefixes are stripped (case-insensitively) from the very start of the input,
// longest first so that "remind me to" wins over "remind me".
var prefixes = [][]string{
	{"set", "me", "a", "reminder", "for"},
	{"set", "me", "reminder", "for"},
	{"set", "a", "reminder", "for"},
	{"set", "reminder", "for"},
	{"remind", "me", "to"},
	{"remind", "me"},
}

// colonPrefixes are single-token prefixes that end in a colon.
var colonPrefixes = []string{"reminder:", "todo:"}

// Parse extracts text, tags and (in later tasks) a due time from input.
func Parse(input string, now time.Time, cfg Config) Result {
	toks := tokenize(input)
	toks = stripPrefix(toks)
	toks, tags := extractTags(toks)

	due, rrule, rest := parseWhen(toks, now, cfg)
	if rest == nil && due == nil && rrule == "" {
		rest = toks
	}

	words := make([]string, 0, len(rest))
	for _, t := range rest {
		words = append(words, t.Text)
	}
	return Result{
		Text:  strings.Join(words, " "),
		Tags:  tags,
		DueAt: due,
		RRule: rrule,
	}
}

// parseWhen is the hook for time phrases (implemented in when.go by a later
// task). It receives the tokens left after prefix and tag removal and returns
// the due time (UTC) and RRULE it found, plus the tokens that remain as item
// text with the time phrase removed. Quoted tokens must never be consumed.
// Returning (nil, "", nil) means "no time phrase": Parse then keeps all tokens.
func parseWhen(tokens []token, now time.Time, cfg Config) (due *time.Time, rrule string, rest []token) {
	return nil, "", nil
}

// tokenize splits input on whitespace, treating a double-quoted segment that
// starts at a token boundary as one literal token (quotes removed). An
// unclosed quote is treated as ordinary text.
func tokenize(s string) []token {
	var toks []token
	i := 0
	for i < len(s) {
		c := s[i]
		if isSpace(c) {
			i++
			continue
		}
		if c == '"' {
			if end := strings.IndexByte(s[i+1:], '"'); end >= 0 {
				toks = append(toks, token{Text: s[i+1 : i+1+end], Quoted: true})
				i += end + 2
				continue
			}
		}
		j := i
		for j < len(s) && !isSpace(s[j]) {
			j++
		}
		toks = append(toks, token{Text: s[i:j]})
		i = j
	}
	return toks
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// stripPrefix removes one leading prefix phrase made of unquoted tokens.
func stripPrefix(toks []token) []token {
	for _, p := range prefixes {
		if len(toks) < len(p) {
			continue
		}
		match := true
		for i, w := range p {
			if toks[i].Quoted || !strings.EqualFold(toks[i].Text, w) {
				match = false
				break
			}
		}
		if match {
			return toks[len(p):]
		}
	}
	if len(toks) > 0 && !toks[0].Quoted {
		lower := strings.ToLower(toks[0].Text)
		for _, p := range colonPrefixes {
			if strings.HasPrefix(lower, p) {
				rem := toks[0].Text[len(p):]
				if rem == "" {
					return toks[1:]
				}
				return append([]token{{Text: rem}}, toks[1:]...)
			}
		}
	}
	return toks
}
