package voice

import (
	"strings"
	"unicode"
)

// promptSentences is the fixed passage every transcription starts its
// prompt with. Whisper reads a prompt as earlier transcript, so reminder
// phrasing, Indian names and terms, and times written as digits make it
// expect (and spell) those. It holds no personal data.
var promptSentences = []string{
	"Remind me to call Rahul tomorrow at 9:30 am.",
	"Pay the electricity bill by Friday, high priority.",
	"Follow up with Priya Sharma about the GST invoice next Monday at 5 pm.",
	"Ask Ankit to review the Jira ticket in 2 hours.",
	"Book a cab to Bengaluru airport on Sunday evening.",
}

// Prompt budget: whisper keeps the last n_text_ctx/2 = 224 prompt tokens;
// 600 characters stays under that even at 2.7 characters per token.
const (
	maxPromptLen = 600
	maxRecentLen = 200
)

// BuildPrompt is the whisper prompt for one transcription: the fixed
// passage, then "Words I use: …" (words is a NormalizeWords list), then the
// recent dictated text. Whisper keeps the end of a long prompt, so the most
// specific context goes last; over the budget, recent is cut to its last
// 200 characters and whole sentences of the fixed passage are dropped from
// its start.
func BuildPrompt(words, recent string) string {
	var tail []string
	if w := strings.TrimSpace(words); w != "" {
		tail = append(tail, "Words I use: "+strings.TrimSuffix(w, ".")+".")
	}
	if r := lastChars(strings.Join(strings.Fields(recent), " "), maxRecentLen); r != "" {
		tail = append(tail, r)
	}
	end := strings.Join(tail, " ")
	room := maxPromptLen - len(end)
	if end != "" {
		room-- // the space before end
	}
	var head []string
	for i := len(promptSentences) - 1; i >= 0; i-- {
		need := len(promptSentences[i])
		if len(head) > 0 {
			need++
		}
		if need > room {
			break
		}
		room -= need
		head = append([]string{promptSentences[i]}, head...)
	}
	return strings.TrimSpace(strings.Join(head, " ") + " " + end)
}

// lastChars is the end of s, at most n bytes, starting at a word.
func lastChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[i+1:]
	} else {
		// One huge "word": drop a partial UTF-8 sequence at the start.
		s = strings.ToValidUTF8(s, "")
	}
	return s
}

// IsPromptEcho reports whether text is just one of the fixed passage's
// sentences, which whisper sometimes repeats on silence.
func IsPromptEcho(text string) bool {
	n := promptKey(text)
	if n == "" {
		return false
	}
	for _, s := range promptSentences {
		if promptKey(s) == n {
			return true
		}
	}
	return false
}

// promptKey is s lowercased with only letters, digits and single spaces.
func promptKey(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return unicode.ToLower(r)
		case unicode.IsSpace(r):
			return ' '
		}
		return -1
	}, s)
	return strings.Join(strings.Fields(s), " ")
}
