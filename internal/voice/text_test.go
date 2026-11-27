package voice

import "testing"

func TestCleanTranscript(t *testing.T) {
	cases := []struct{ in, want string }{
		{" Remind me tomorrow at 5 p.m. to call Sam.\n", "Remind me tomorrow at 5 pm to call Sam"},
		{"[BLANK_AUDIO]", ""},
		{" (wind blowing)", ""},
		{" *coughs*", ""},
		{"", ""},
		{"Fix the build hashtag backend.", "Fix the build #backend"},
		{"deploy at 9 a.m. hash tag Ops", "deploy at 9 am #ops"},
		{"line one\n line two", "line one line two"},
		{"Is it done?", "Is it done?"},
		{"Check [MUSIC] logs", "Check logs"},
		{"Call at 5 P.M.", "Call at 5 pm"},
		{"Review the PR, then merge.", "Review the PR then merge"},
		{"In two hours, check the build.", "In 2 hours check the build"},
		{"Tomorrow at five p.m. call Sam", "Tomorrow at 5 pm call Sam"},
		{"at three o'clock call Bob", "at 3 call Bob"},
		{"in ten minutes stretch", "in 10 minutes stretch"},
		{"Every Monday at nine thirty standup", "Every Monday at 9:30 standup"},
		{"Call one person about the two bugs", "Call one person about the two bugs"},
		{"In an hour, coffee.", "In an hour coffee"},
	}
	for _, c := range cases {
		if got := CleanTranscript(c.in); got != c.want {
			t.Errorf("CleanTranscript(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
