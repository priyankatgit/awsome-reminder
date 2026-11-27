package reply

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestGuess(t *testing.T) {
	cases := []struct {
		name, text, process, title string
		owedTo, source             string
	}{
		{"outlook from line", "From: Priya Sharma <p@x.com>\nSent: Monday\ncan you check", "OUTLOOK.EXE", "Inbox - me@x.com - Outlook", "Priya Sharma", "Outlook"},
		{"teams chat header", "Sam Lee  10:42 AM\nship it?", "ms-teams.exe", "Chat | Ops | Microsoft Teams", "Sam Lee", "Teams"},
		{"teams title", "deploy?", "ms-teams.exe", "Chat | Priya Sharma | Microsoft Teams", "Priya Sharma", "Teams"},
		{"slack dm title", "deploy?", "slack.exe", "Priya (DM) - acme - Slack", "Priya", "Slack"},
		{"browser no person", "hello", "chrome.exe", "Inbox (3) - Gmail", "", "Chrome"},
		{"empty text", "", "notepad.exe", "x", "", "notepad"},
		{"from line without address", "From: Sam Lee\nhi", "OUTLOOK.EXE", "", "Sam Lee", "Outlook"},
		{"comma header", "Sam Lee, 10:42\nhi", "slack.exe", "", "Sam Lee", "Slack"},
		{"dash header", "Sam Lee — 10:42\nhi", "slack.exe", "", "Sam Lee", "Slack"},
		{"teams bare title", "hi", "Teams.exe", "Priya Sharma | Microsoft Teams", "Priya Sharma", "Teams"},
		{"slack channel title", "hi", "slack.exe", "Priya - acme - Slack", "Priya", "Slack"},
		{"short text with time is not a header", "Sounds good, 5:30", "ms-teams.exe", "Chat | Priya Sharma | Microsoft Teams", "Priya Sharma", "Teams"},
		{"short text with dash time, no title", "Call me - 10:30", "slack.exe", "", "", "Slack"},
		{"body from line ignored", "Sam Lee  10:42 AM\nok\nsee notes\nmore\nfrom: staging", "ms-teams.exe", "Chat | Ops | Microsoft Teams", "Sam Lee", "Teams"},
		{"late From line ignored, title wins", "deploy now\nok\nthanks\nFrom: staging", "ms-teams.exe", "Chat | Priya Sharma | Microsoft Teams", "Priya Sharma", "Teams"},
		{"outlook message window", "hi", "OUTLOOK.EXE", "Budget review - Message (HTML)", "", "Outlook"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Guess(c.text, c.process, c.title)
			if got.OwedTo != c.owedTo || got.Source != c.source {
				t.Errorf("Guess = {OwedTo:%q Source:%q}, want {%q %q}", got.OwedTo, got.Source, c.owedTo, c.source)
			}
			if got.Quote != strings.TrimSpace(c.text) {
				t.Errorf("Quote = %q, want trimmed text", got.Quote)
			}
		})
	}
}

func TestSourceName(t *testing.T) {
	cases := map[string]string{
		"OUTLOOK.EXE": "Outlook", "ms-teams.exe": "Teams", "Teams.exe": "Teams",
		"slack.exe": "Slack", "chrome.exe": "Chrome", "msedge.exe": "Edge",
		"firefox.exe": "Firefox", "thunderbird.exe": "Thunderbird",
		"Microsoft Outlook": "Outlook", "Microsoft Teams": "Teams", "Slack": "Slack",
		"Mail": "Mail", "Safari": "Safari", "notepad.exe": "notepad", "code": "code",
		"olk.exe": "Outlook", "OLK.EXE": "Outlook",
	}
	for in, want := range cases {
		if got := SourceName(in); got != want {
			t.Errorf("SourceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLine(t *testing.T) {
	long := strings.Repeat("a", 500)
	cases := []struct {
		name string
		m    Meta
		want string
	}{
		{"person and quote", Meta{OwedTo: "Priya", Quote: "can you check"}, `Reply to Priya: "can you check"`},
		{"no person", Meta{Quote: "can you check"}, `Reply: "can you check"`},
		{"no quote", Meta{OwedTo: "Priya"}, "Reply to Priya"},
		{"long quote cut", Meta{OwedTo: "P", Quote: long}, `Reply to P: "` + strings.Repeat("a", 200) + `…"`},
		{"exactly 200 not cut", Meta{Quote: strings.Repeat("b", 200)}, `Reply: "` + strings.Repeat("b", 200) + `"`},
		{"runes not bytes", Meta{Quote: strings.Repeat("é", 201)}, `Reply: "` + strings.Repeat("é", 200) + `…"`},
		{"newlines become spaces", Meta{OwedTo: "P", Quote: "a\n\n  b\r\nc"}, `Reply to P: "a b c"`},
		{"inner quotes become closing curly quotes", Meta{OwedTo: "P", Quote: `say "yes" or "no"`}, `Reply to P: "say ”yes” or ”no”"`},
		{"a quote mark in the person cannot unbalance the line", Meta{OwedTo: `Sam "Ops`, Quote: "go"}, `Reply to Sam ”Ops: "go"`},
		{"a quote mark in the person alone", Meta{OwedTo: `"Sam"`}, `Reply to ”Sam”`},
		{"nothing", Meta{}, "Reply"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Line(c.m); got != c.want {
				t.Errorf("Line = %q, want %q", got, c.want)
			}
		})
	}
}

func TestQuoteMarkdown(t *testing.T) {
	if got, want := QuoteMarkdown("a\nb"), "> a\n> b"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := QuoteMarkdown("a\r\n\r\nb"), "> a\n> \n> b"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGuessCapsQuote(t *testing.T) {
	big := strings.Repeat("x", MaxQuoteBytes+10)
	got := Guess(big, "", "").Quote
	if want := strings.Repeat("x", MaxQuoteBytes) + "…"; got != want {
		t.Errorf("Guess quote: len %d, want %d (cut + …)", len(got), len(want))
	}
}

func TestCapQuote(t *testing.T) {
	if got := CapQuote("short"); got != "short" {
		t.Errorf("CapQuote(short) = %q", got)
	}
	exact := strings.Repeat("a", MaxQuoteBytes)
	if got := CapQuote(exact); got != exact {
		t.Errorf("CapQuote at the limit changed the text (len %d)", len(got))
	}
	// A 2-byte rune straddling the limit is dropped whole, never split.
	straddle := strings.Repeat("a", MaxQuoteBytes-1) + "é" + "tail"
	got := CapQuote(straddle)
	if want := strings.Repeat("a", MaxQuoteBytes-1) + "…"; got != want {
		t.Errorf("CapQuote(straddle): len %d, want %d", len(got), len(want))
	}
	if !utf8.ValidString(got) {
		t.Error("CapQuote split a rune")
	}
}
