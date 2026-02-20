package utility

import "strings"

func MatchMultiple(input string, matches []string) bool {
	for _, match := range matches {
		if input == match {
			return true
		}
	}
	return false
}

func ReplaceMultiple(str string, oldStrings []string, newString string) string {
	if len(oldStrings) == 0 {
		return str
	}
	if len(str) == 0 {
		return str
	}
	for _, oldStr := range oldStrings {
		str = strings.ReplaceAll(str, oldStr, newString)
	}
	return str
}

func ExtractPairText(text string, lookup string) string {
	if !containsPair(text, lookup) {
		return ""
	}
	firstIndex := strings.Index(text, lookup)
	lastIndex := strings.LastIndex(text, lookup)
	foundText := text[firstIndex : lastIndex+len(lookup)]
	return strings.ReplaceAll(foundText, lookup, "")
}

func containsPair(text string, lookup string) bool {
	return strings.Contains(text, lookup) && strings.Count(text, lookup)%2 == 0
}
