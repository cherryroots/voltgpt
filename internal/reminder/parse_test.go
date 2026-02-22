package reminder

import (
	"testing"
	"time"
)

// testNow is a fixed reference point for all parse tests: 2026-02-22 12:00 UTC.
var testNow = time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC)

func TestParseTimeOffset(t *testing.T) {
	tests := []struct {
		input     string
		wantDelta time.Duration
		wantMsg   string
	}{
		{"in 2h to check the oven", 2 * time.Hour, "check the oven"},
		{"in 30m: buy groceries", 30 * time.Minute, "buy groceries"},
		{"in 2h30m meeting", 2*time.Hour + 30*time.Minute, "meeting"},
		{"in 1 hour dentist", time.Hour, "dentist"},
		{"in 30 minutes call mom", 30 * time.Minute, "call mom"},
		{"in 7 days workout", 7 * 24 * time.Hour, "workout"},
		{"in 1y update CV", 365 * 24 * time.Hour, "update CV"},
		{"in 2 years tax", 2 * 365 * 24 * time.Hour, "tax"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, remaining, err := ParseTime(tt.input, testNow)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			delta := got.Sub(testNow)
			if delta != tt.wantDelta {
				t.Errorf("delta: got %v, want %v", delta, tt.wantDelta)
			}
			if remaining != tt.wantMsg {
				t.Errorf("remaining: got %q, want %q", remaining, tt.wantMsg)
			}
		})
	}
}

func TestParseTimeAbsolute(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	tests := []struct {
		input   string
		wantT   time.Time
		wantMsg string
	}{
		{
			"at 2026-03-01 15:04 check it",
			time.Date(2026, 3, 1, 15, 4, 0, 0, time.UTC),
			"check it",
		},
		{
			"at 2026-03-01 15:04 EST check it",
			time.Date(2026, 3, 1, 15, 4, 0, 0, est),
			"check it",
		},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, remaining, err := ParseTime(tt.input, testNow)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.wantT) {
				t.Errorf("time: got %v, want %v", got, tt.wantT)
			}
			if remaining != tt.wantMsg {
				t.Errorf("remaining: got %q, want %q", remaining, tt.wantMsg)
			}
		})
	}
}

func TestParseTimeTimeOnly(t *testing.T) {
	// testNow is 12:00 UTC — "at 15:00" is still in the future today.
	got, _, err := ParseTime("at 15:00 reminder", testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 2, 22, 15, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("future today: got %v, want %v", got, want)
	}

	// "at 10:00" is in the past today → should roll to tomorrow.
	got2, _, err := ParseTime("at 10:00 reminder", testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want2 := time.Date(2026, 2, 23, 10, 0, 0, 0, time.UTC)
	if !got2.Equal(want2) {
		t.Errorf("past → tomorrow: got %v, want %v", got2, want2)
	}
}

func TestParseTimeErrors(t *testing.T) {
	if _, _, err := ParseTime("remind me something", testNow); err == nil {
		t.Error("expected error for missing 'in'/'at' prefix")
	}
	if _, _, err := ParseTime("in nothing", testNow); err == nil {
		t.Error("expected error for no valid duration units")
	}
	if _, _, err := ParseTime("at invalid-date here", testNow); err == nil {
		t.Error("expected error for unparseable absolute time")
	}
}
