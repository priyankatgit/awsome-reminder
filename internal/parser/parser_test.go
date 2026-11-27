package parser

import (
	"reflect"
	"testing"
	"time"
	_ "time/tzdata"
)

func testNow(t *testing.T) (time.Time, Config) {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, loc)
	cfg := Config{
		Loc:          loc,
		DefaultTime:  Clock{9, 0},
		NextWeekDay:  time.Monday,
		NextWeekTime: Clock{9, 0},
	}
	return now, cfg
}

type parseCase struct {
	name, in, text string
	tags           []string
}

func runCases(t *testing.T, cases []parseCase) {
	now, cfg := testNow(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Parse(c.in, now, cfg)
			if r.Text != c.text {
				t.Errorf("Text = %q, want %q", r.Text, c.text)
			}
			if r.Tags == nil || !reflect.DeepEqual(r.Tags, c.tags) {
				t.Errorf("Tags = %#v, want %#v", r.Tags, c.tags)
			}
			if r.DueAt != nil {
				t.Errorf("DueAt = %v, want nil", r.DueAt)
			}
		})
	}
}

func TestParsePrefixesAndTags(t *testing.T) {
	runCases(t, []parseCase{
		{"plain", "buy milk", "buy milk", []string{}},
		{"remind me to", "remind me to call mom", "call mom", []string{}},
		{"set me a reminder for", "set me a reminder for standup notes", "standup notes", []string{}},
		{"set me reminder for caps", "Set me reminder for X", "X", []string{}},
		{"blank", "   ", "", []string{}},
		{"remind me", "remind me call mom", "call mom", []string{}},
		{"set a reminder for", "Set A Reminder For lunch", "lunch", []string{}},
		{"set reminder for", "set reminder for lunch", "lunch", []string{}},
		{"reminder colon", "reminder: pay rent", "pay rent", []string{}},
		{"todo colon", "TODO: pay rent", "pay rent", []string{}},
		{"prefix only at start", "please remind me to x", "please remind me to x", []string{}},
		{"whitespace collapsed", "  buy   milk \t now ", "buy milk now", []string{}},
	})
}

func TestParseTags(t *testing.T) {
	runCases(t, []parseCase{
		{"two tags", "fix flaky test #backend #CI", "fix flaky test", []string{"backend", "ci"}},
		{"non-tags", "fix bug #123 in C# parser #backend", "fix bug #123 in C# parser", []string{"backend"}},
		{"dedupe", "#ops restart #ops worker", "restart worker", []string{"ops"}},
		{"dedupe case", "a #Ops b #OPS", "a b", []string{"ops"}},
		{"tag chars", "x #a_b-c1", "x", []string{"a_b-c1"}},
		{"tag after prefix", "remind me to #home water plants", "water plants", []string{"home"}},
	})
}

func TestParseQuotes(t *testing.T) {
	runCases(t, []parseCase{
		{"quoted tag literal", `"#notatag" literal`, "#notatag literal", []string{}},
		{"quoted prefix literal", `"remind me to" x`, "remind me to x", []string{}},
		{"quoted spaces", `say "hello   world" #t`, "say hello   world", []string{"t"}},
		{"unclosed quote", `say "hello #t`, `say "hello`, []string{"t"}},
	})
}

func TestEmptyQuotes(t *testing.T) {
	runCases(t, []parseCase{
		{"empty quotes", `a "" b`, "a b", []string{}},
	})
}

func TestTokenizeQuoted(t *testing.T) {
	got := tokenize(`a "b c" d`)
	want := []token{{"a", false}, {"b c", true}, {"d", false}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokenize = %#v, want %#v", got, want)
	}
}

// Dictation (Phase 1.5) puts the time before the task: "remind me tomorrow
// at 5 pm to call Sam". The "to" that joined them is not part of the task.
func TestParseRemindMeWhenTo(t *testing.T) {
	now, cfg := testNow(t)
	cases := []struct{ in, text string }{
		{"Remind me tomorrow at 5 pm to call Sam", "call Sam"},
		{"remind me tomorrow to check the deploy #backend", "check the deploy"},
		{"set a reminder for friday 3pm to demo", "demo"},
		{"tomorrow to-do list review", "to-do list review"},
		{"tomorrow to Paris trip prep", "to Paris trip prep"},
		{`remind me tomorrow "to" x`, "to x"},
	}
	for _, c := range cases {
		r := Parse(c.in, now, cfg)
		if r.Text != c.text {
			t.Errorf("Parse(%q).Text = %q, want %q", c.in, r.Text, c.text)
		}
		if r.DueAt == nil {
			t.Errorf("Parse(%q): want a due time", c.in)
		}
	}
}
