package voice

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// Non-speech tags such as [BLANK_AUDIO] or [MUSIC].
	bracketed = regexp.MustCompile(`\[[^\]]*\]`)
	// A transcript that is only an annotation: "(wind blowing)", "*coughs*".
	annotation = regexp.MustCompile(`^(\([^)]*\)|\*[^*]*\*)$`)
	// "5 p.m." -> "5 pm" so the capture parser understands it.
	meridiem = regexp.MustCompile(`(?i)\b([ap])\.m\.`)
	// "hashtag backend" / "hash tag backend" -> "#backend".
	spokenTag = regexp.MustCompile(`(?i)\bhash ?tag\s+([\p{L}\p{N}_-]+)`)
)

// CleanTranscript turns whisper's raw text into one capture line: no
// non-speech tags, one line, "a.m."/"p.m." as am/pm, spoken hashtags as
// #tags, and no trailing full stop (a to-do is not a sentence). It returns
// "" when nothing was said.
func CleanTranscript(s string) string {
	s = bracketed.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	if annotation.MatchString(s) {
		return ""
	}
	s = meridiem.ReplaceAllStringFunc(s, func(m string) string {
		return strings.ToLower(m[:1]) + "m"
	})
	s = spokenTag.ReplaceAllStringFunc(s, func(m string) string {
		w := spokenTag.FindStringSubmatch(m)[1]
		return "#" + strings.ToLower(w)
	})
	// Whisper punctuates speech; commas split time phrases for the parser
	// ("In 2 hours, check …").
	s = strings.ReplaceAll(s, ",", "")
	s = oclock.ReplaceAllString(s, "")
	s = spokenNumbers(s)
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, ".")
	return s
}

// hallucinations are what whisper makes up from silence or noise.
var hallucinations = map[string]bool{
	"thank you": true, "thanks for watching": true,
	"thank you for watching": true, "you": true, "bye": true,
}

// DropHallucination returns "" when a segment with under 1.5 s of speech is
// only one of whisper's known silence hallucinations; otherwise text.
func DropHallucination(text string, speechMs int) string {
	if speechMs >= 1500 {
		return text
	}
	n := strings.Map(func(r rune) rune {
		if strings.ContainsRune(".,!?", r) {
			return -1
		}
		return r
	}, strings.ToLower(text))
	if hallucinations[strings.TrimSpace(n)] {
		return ""
	}
	return text
}

var oclock = regexp.MustCompile(`(?i)\s*o['’]clock\b`)

var smallNumbers = map[string]int{
	"one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7,
	"eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13,
	"fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18,
	"nineteen": 19, "twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
}

// wordNumber reads "five", "twenty", "forty-five".
func wordNumber(w string) (int, bool) {
	w = strings.ToLower(w)
	if n, ok := smallNumbers[w]; ok {
		return n, true
	}
	tens, unit, ok := strings.Cut(w, "-")
	if !ok {
		return 0, false
	}
	t, ok1 := smallNumbers[tens]
	u, ok2 := smallNumbers[unit]
	if !ok1 || !ok2 || t < 20 || t%10 != 0 || u > 9 {
		return 0, false
	}
	return t + u, true
}

var (
	numberBefore = map[string]bool{"at": true, "in": true, "by": true}
	numberAfter  = map[string]bool{
		"am": true, "pm": true, "min": true, "mins": true, "minute": true, "minutes": true,
		"hour": true, "hours": true, "day": true, "days": true, "week": true, "weeks": true,
	}
)

// spokenNumbers writes number words as digits where they are part of a
// time ("at five pm", "in two hours", "at nine thirty" -> "at 9:30") and
// leaves other numbers alone ("call one person").
func spokenNumbers(s string) string {
	words := strings.Fields(s)
	out := make([]string, 0, len(words))
	for i := 0; i < len(words); i++ {
		n, ok := wordNumber(words[i])
		if !ok {
			out = append(out, words[i])
			continue
		}
		prev, next := "", ""
		if i > 0 {
			prev = strings.ToLower(words[i-1])
		}
		if i+1 < len(words) {
			next = strings.ToLower(words[i+1])
		}
		if prev == "at" && n >= 1 && n <= 12 {
			if m, ok := wordNumber(next); ok && m >= 10 && m <= 59 {
				out = append(out, fmt.Sprintf("%d:%02d", n, m))
				i++
				continue
			}
		}
		if numberBefore[prev] || numberAfter[next] {
			out = append(out, strconv.Itoa(n))
			continue
		}
		out = append(out, words[i])
	}
	return strings.Join(out, " ")
}
