package main

import (
	"testing"
	"time"
)

func TestDisplayTimeUsesConfiguredZoneAndDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ utc, want string }{
		{"2026-01-25T16:00:00Z", "01/25/2026 11:00"},
		{"2026-10-25T16:00:00Z", "10/25/2026 12:00"},
	} {
		timestamp, err := time.Parse(time.RFC3339, tc.utc)
		if err != nil {
			t.Fatal(err)
		}
		if got := formatDisplayTime(timestamp, location); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}
