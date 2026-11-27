// Package reply guesses who a message came from and where, so the "reply owed"
// capture box can be pre-filled from a grabbed message and the foreground window.
package reply

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Meta is what we could infer about a message someone is waiting on.
type Meta struct{ OwedTo, Source, Quote string }

const quoteRunes = 200

var (
	fromLineRe = regexp.MustCompile(`^From:\s*([^<]+?)\s*(?:<[^>]*>)?$`)
	// "Name  10:42 AM", "Name, 10:42", "Name — 10:42".
	chatHeaderRe = regexp.MustCompile(`^([^\r\n:]{1,60}?)(?:\s{2,}|\s*,\s*|\s+[—–-]\s+)\d{1,2}:\d{2}(?:\s*[AaPp][Mm])?\s*$`)

	teamsChatRe = regexp.MustCompile(`^Chat \| (.+?) \| Microsoft Teams$`)
	teamsBareRe = regexp.MustCompile(`^(.+?) \| Microsoft Teams$`)
	slackDMRe   = regexp.MustCompile(`^(.+?) \(DM\) - .+ - Slack$`)
	slackRe     = regexp.MustCompile(`^(.+?) - .+ - Slack$`)

	spaceRe = regexp.MustCompile(`\s+`)
)

var sourceNames = map[string]string{
	"outlook.exe":       "Outlook",
	"ms-teams.exe":      "Teams",
	"teams.exe":         "Teams",
	"slack.exe":         "Slack",
	"chrome.exe":        "Chrome",
	"msedge.exe":        "Edge",
	"firefox.exe":       "Firefox",
	"thunderbird.exe":   "Thunderbird",
	"microsoft outlook": "Outlook",
	"microsoft teams":   "Teams",
	"slack":             "Slack",
	"mail":              "Mail",
	"safari":            "Safari",
}

// Guess infers who the message is from and which app it came from.
// A person found in the text wins over one found in the window title.
func Guess(text, process, title string) Meta {
	quote := strings.TrimSpace(text)
	owed := personFromText(quote)
	if owed == "" {
		owed = personFromTitle(title)
	}
	return Meta{OwedTo: owed, Source: SourceName(process), Quote: quote}
}

func personFromText(text string) string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	// A From: line only counts near the top; later ones are quoted body text.
	for i := 0; i < len(lines) && i < 3; i++ {
		if m := fromLineRe.FindStringSubmatch(lines[i]); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	// A chat header needs a message body under it and a name-like shape.
	if len(lines) >= 2 {
		if m := chatHeaderRe.FindStringSubmatch(lines[0]); m != nil {
			if name := strings.TrimSpace(m[1]); nameLike(name) {
				return name
			}
		}
	}
	return ""
}

func nameLike(s string) bool {
	if len(strings.Fields(s)) > 4 || strings.ContainsAny(s, ".?!") {
		return false
	}
	r := []rune(s)
	return len(r) > 0 && unicode.IsLetter(r[0])
}

func personFromTitle(title string) string {
	for _, re := range []*regexp.Regexp{teamsChatRe, teamsBareRe, slackDMRe, slackRe} {
		if m := re.FindStringSubmatch(title); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// SourceName turns a process name into a friendly app name.
func SourceName(process string) string {
	if n, ok := sourceNames[strings.ToLower(process)]; ok {
		return n
	}
	return strings.TrimSuffix(process, filepath.Ext(process))
}

// Line builds the one-line task title for a reply owed.
func Line(m Meta) string {
	quote := spaceRe.ReplaceAllString(strings.TrimSpace(m.Quote), " ")
	if r := []rune(quote); len(r) > quoteRunes {
		quote = string(r[:quoteRunes]) + "…"
	}
	switch {
	case m.OwedTo != "" && quote != "":
		return "Reply to " + m.OwedTo + ": " + quote
	case m.OwedTo != "":
		return "Reply to " + m.OwedTo
	case quote != "":
		return "Reply: " + quote
	}
	return "Reply"
}

// QuoteMarkdown prefixes every line of text with "> ".
func QuoteMarkdown(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}
