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
		{"Remind me to call Venkatesh tomorrow at 9.30 a.m. about the Aadhar update.", "Remind me to call Venkatesh tomorrow at 9:30 am about the Aadhar update"},
		{"standup at 10.15", "standup at 10:15"},
		{"Pay 2.50 to Ravi", "Pay 2.50 to Ravi"},
		{"version 1.20 ships", "version 1.20 ships"},
	}
	for _, c := range cases {
		if got := CleanTranscript(c.in); got != c.want {
			t.Errorf("CleanTranscript(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDropHallucination(t *testing.T) {
	for _, c := range []struct {
		in   string
		ms   int
		want string
	}{
		{"Thank you.", 900, ""},
		{"you", 400, ""},
		{"Thank you.", 2000, "Thank you."},
		{"thank you Sam", 900, "thank you Sam"},
		{"Bye!", 1000, ""},
	} {
		if got := DropHallucination(c.in, c.ms); got != c.want {
			t.Errorf("DropHallucination(%q, %d) = %q, want %q", c.in, c.ms, got, c.want)
		}
	}
}
