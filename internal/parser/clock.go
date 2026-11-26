package parser

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	ampmRe  = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?(am|pm)$`)
	hmRe    = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?$`)
	clock24 = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
)

// word returns the lowercased text of toks[i] when it exists and is unquoted.
func word(toks []token, i int) (string, bool) {
	if i < 0 || i >= len(toks) || toks[i].Quoted {
		return "", false
	}
	return strings.ToLower(toks[i].Text), true
}

func isConnector(s string) bool {
	switch strings.ToLower(s) {
	case "at", "on", "for", "by":
		return true
	}
	return false
}

// matchClock recognises a time of day starting at toks[i] (with an optional
// leading "at") and returns the clock and the number of tokens consumed.
func matchClock(toks []token, i int) (Clock, int, clockKind, bool) {
	pos := i
	at := false
	if w, ok := word(toks, pos); ok && w == "at" {
		at = true
		pos++
	}
	w, ok := word(toks, pos)
	if !ok {
		return Clock{}, 0, 0, false
	}
	n := pos - i + 1
	switch w {
	case "noon":
		return Clock{12, 0}, n, ckExplicit, true
	case "midnight":
		return Clock{0, 0}, n, ckExplicit, true
	case "eod":
		return Clock{17, 0}, n, ckExplicit, true
	}
	if m := ampmRe.FindStringSubmatch(w); m != nil {
		c, k, ok := ampmClock(m[1], m[2], m[3], n)
		return c, k, ckExplicit, ok
	}
	if m := hmRe.FindStringSubmatch(w); m != nil {
		if nxt, ok := word(toks, pos+1); ok && (nxt == "am" || nxt == "pm") {
			c, k, ok := ampmClock(m[1], m[2], nxt, n+1)
			return c, k, ckExplicit, ok
		}
	}
	if m := clock24.FindStringSubmatch(w); m != nil {
		h, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if h < 24 && min < 60 {
			kind := ckBareColon
			if at {
				kind = ckPlain
			}
			return Clock{h, min}, n, kind, true
		}
		return Clock{}, 0, 0, false
	}
	if at && hmRe.MatchString(w) && !strings.Contains(w, ":") {
		h, _ := strconv.Atoi(w)
		switch {
		case h >= 1 && h <= 7:
			return Clock{h + 12, 0}, n, ckWeakAt, true
		case h >= 8 && h <= 23:
			return Clock{h, 0}, n, ckWeakAt, true
		}
	}
	return Clock{}, 0, 0, false
}

func ampmClock(hs, ms, ampm string, n int) (Clock, int, bool) {
	h, _ := strconv.Atoi(hs)
	min := 0
	if ms != "" {
		min, _ = strconv.Atoi(ms)
	}
	if h < 1 || h > 12 || min > 59 {
		return Clock{}, 0, false
	}
	h %= 12
	if ampm == "pm" {
		h += 12
	}
	return Clock{h, min}, n, true
}

// clockKind says how trustworthy a recognised time of day is.
type clockKind int

const (
	ckExplicit  clockKind = iota // am/pm, noon, midnight, eod
	ckPlain                      // "at 17:30"
	ckBareColon                  // "9:30": only counts next to a date or recurrence
	ckWeakAt                     // "at 5": only counts when nothing stronger exists
)
