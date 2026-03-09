package memory

import "fmt"

type profileFactCounts struct {
	Bio           int
	Interests     int
	Skills        int
	Opinions      int
	Relationships int
	Other         int
	Total         int
}

type profileCompactionStats struct {
	Before            profileFactCounts
	After             profileFactCounts
	TextFactsTrimmed  int
	SourceRefsTrimmed int
}

func countProfileFacts(profile GuildUserProfile) profileFactCounts {
	counts := profileFactCounts{
		Bio:           len(profile.Bio),
		Interests:     len(profile.Interests),
		Skills:        len(profile.Skills),
		Opinions:      len(profile.Opinions),
		Relationships: len(profile.Relationships),
		Other:         len(profile.Other),
	}
	counts.Total = counts.Bio + counts.Interests + counts.Skills + counts.Opinions + counts.Relationships + counts.Other
	return counts
}

func countRenderedUserFacts(users []renderedUser) profileFactCounts {
	var total profileFactCounts
	for _, user := range users {
		counts := countProfileFacts(*user.Profile)
		total.Bio += counts.Bio
		total.Interests += counts.Interests
		total.Skills += counts.Skills
		total.Opinions += counts.Opinions
		total.Relationships += counts.Relationships
		total.Other += counts.Other
		total.Total += counts.Total
	}
	return total
}

func profileCountLogFields(prefix string, counts profileFactCounts) string {
	return fmt.Sprintf(
		"%sbio=%d %sinterests=%d %sskills=%d %sopinions=%d %srelationships=%d %sother=%d %stotal=%d",
		prefix, counts.Bio,
		prefix, counts.Interests,
		prefix, counts.Skills,
		prefix, counts.Opinions,
		prefix, counts.Relationships,
		prefix, counts.Other,
		prefix, counts.Total,
	)
}

func (s profileCompactionStats) factsDropped() int {
	return s.Before.Total - s.After.Total
}

func (s profileCompactionStats) changed() bool {
	return s.factsDropped() > 0 || s.TextFactsTrimmed > 0 || s.SourceRefsTrimmed > 0
}
