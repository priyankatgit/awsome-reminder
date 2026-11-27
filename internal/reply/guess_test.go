package reply

import (
	"strings"
	"testing"
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
		{"person and quote", Meta{OwedTo: "Priya", Quote: "can you check"}, "Reply to Priya: can you check"},
		{"no person", Meta{Quote: "can you check"}, "Reply: can you check"},
		{"no quote", Meta{OwedTo: "Priya"}, "Reply to Priya"},
		{"long quote cut", Meta{OwedTo: "P", Quote: long}, "Reply to P: " + strings.Repeat("a", 200) + "…"},
		{"exactly 200 not cut", Meta{Quote: strings.Repeat("b", 200)}, "Reply: " + strings.Repeat("b", 200)},
		{"runes not bytes", Meta{Quote: strings.Repeat("é", 201)}, "Reply: " + strings.Repeat("é", 200) + "…"},
		{"newlines become spaces", Meta{OwedTo: "P", Quote: "a\n\n  b\r\nc"}, "Reply to P: a b c"},
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
