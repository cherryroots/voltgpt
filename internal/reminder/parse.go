package reminder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// unitRe matches one duration component: a number followed by a unit.
// Longer unit names (e.g. "months") must precede shorter prefixes (e.g. "mo", "m")
// to avoid greedy mismatches.
var unitRe = regexp.MustCompile(
	`(?i)^(\d+)\s*` +
		`(years?|yr|months?|mo|weeks?|wk|days?|hours?|hr|minutes?|mins?|seconds?|secs?|[ymwdhs])`,
)

// tzAbbrevs maps common timezone abbreviations to fixed-offset locations.
// time.LoadLocation handles IANA names (e.g. "America/New_York"); this map
// covers short abbreviations that LoadLocation does not recognise.
var tzAbbrevs = map[string]*time.Location{
	"UTC":  time.UTC,
	"GMT":  time.UTC,
	"EST":  time.FixedZone("EST", -5*3600),
	"EDT":  time.FixedZone("EDT", -4*3600),
	"CST":  time.FixedZone("CST", -6*3600),
	"CDT":  time.FixedZone("CDT", -5*3600),
	"MST":  time.FixedZone("MST", -7*3600),
	"MDT":  time.FixedZone("MDT", -6*3600),
	"PST":  time.FixedZone("PST", -8*3600),
	"PDT":  time.FixedZone("PDT", -7*3600),
	"CET":  time.FixedZone("CET", 1*3600),
	"CEST": time.FixedZone("CEST", 2*3600),
	"BST":  time.FixedZone("BST", 1*3600),
	"JST":  time.FixedZone("JST", 9*3600),
	"AEST": time.FixedZone("AEST", 10*3600),
}

// Trigger reports whether content starts with a reminder trigger phrase.
// Returns the byte length of the trigger prefix so the caller can slice past it.
func Trigger(content string) (int, bool) {
	lower := strings.ToLower(strings.TrimSpace(content))
	for _, trigger := range []string{"remind me ", "reminder ", "remind "} {
		if strings.HasPrefix(lower, trigger) {
			return len(trigger), true
		}
	}
	return 0, false
}

// ParseTime parses a time expression from the start of s.
// s must begin with "in " (relative offset) or "at " (absolute datetime).
// Returns the resolved fire time, the remainder of s as the reminder message,
// and any parse error.
func ParseTime(s string, now time.Time) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "in "):
		return parseOffset(s[3:], now)
	case strings.HasPrefix(lower, "at "):
		return parseAbsolute(s[3:], now)
	default:
		return time.Time{}, s, fmt.Errorf("time expression must start with 'in' or 'at'")
	}
}

// parseOffset handles relative offsets: "2h30m to check the oven", "4 years tax".
// Multiple unit tokens are consumed greedily until no more unit tokens are found.
func parseOffset(s string, now time.Time) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	t := now
	matched := false

	for {
		m := unitRe.FindStringSubmatch(s)
		if m == nil {
			break
		}
		n, _ := strconv.Atoi(m[1])
		unit := strings.ToLower(m[2])
		switch {
		case strings.HasPrefix(unit, "y"):
			t = t.AddDate(n, 0, 0)
		case strings.HasPrefix(unit, "mo"):
			t = t.AddDate(0, n, 0)
		case strings.HasPrefix(unit, "w"):
			t = t.AddDate(0, 0, n*7)
		case strings.HasPrefix(unit, "d"):
			t = t.AddDate(0, 0, n)
		case strings.HasPrefix(unit, "h"):
			t = t.Add(time.Duration(n) * time.Hour)
		case strings.HasPrefix(unit, "mi"), unit == "m":
			t = t.Add(time.Duration(n) * time.Minute)
		case strings.HasPrefix(unit, "s"):
			t = t.Add(time.Duration(n) * time.Second)
		}
		s = strings.TrimSpace(s[len(m[0]):])
		matched = true
	}

	if !matched {
		return time.Time{}, s, fmt.Errorf("no valid duration found in %q", s)
	}
	return t, cleanRemaining(s), nil
}

// parseAbsolute handles "2026-03-01 15:04 EST: text" and "15:04 UTC text".
// It tries combining 1 or 2 leading words as the datetime string, optionally
// followed by a timezone word.
func parseAbsolute(s string, now time.Time) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	words := strings.Fields(s)
	if len(words) == 0 {
		return time.Time{}, s, fmt.Errorf("empty absolute time expression")
	}

	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"15:04:05",
		"15:04",
	}

	// Try 2-word then 1-word datetime strings.
	for numTimeWords := 2; numTimeWords >= 1; numTimeWords-- {
		if numTimeWords > len(words) {
			continue
		}
		timeStr := strings.Join(words[:numTimeWords], " ")

		// Check whether the next word is a recognized timezone.
		loc := time.UTC
		tzWords := 0
		if numTimeWords < len(words) {
			candidate := words[numTimeWords]
			if l, err := time.LoadLocation(candidate); err == nil {
				loc = l
				tzWords = 1
			} else if l, ok := tzAbbrevs[strings.ToUpper(candidate)]; ok {
				loc = l
				tzWords = 1
			}
		}

		for _, layout := range layouts {
			t, err := time.ParseInLocation(layout, timeStr, loc)
			if err != nil {
				continue
			}

			// Time-only input: anchor to today; roll forward to tomorrow if past.
			if !strings.Contains(timeStr, "-") {
				y, mo, d := now.In(loc).Date()
				t = time.Date(y, mo, d, t.Hour(), t.Minute(), t.Second(), 0, loc)
				if !t.After(now) {
					t = t.AddDate(0, 0, 1)
				}
			}

			consumed := numTimeWords + tzWords
			remaining := ""
			if consumed < len(words) {
				remaining = strings.Join(words[consumed:], " ")
			}
			return t, cleanRemaining(remaining), nil
		}
	}

	return time.Time{}, s, fmt.Errorf("could not parse time from %q", s)
}

// cleanRemaining strips leading punctuation and optional "to " prefix from
// the remainder string, which becomes the reminder message text.
func cleanRemaining(s string) string {
	s = strings.TrimLeft(s, ":-, \t")
	if strings.HasPrefix(strings.ToLower(s), "to ") {
		s = s[3:]
	}
	return strings.TrimSpace(s)
}
