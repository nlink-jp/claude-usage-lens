package cmd

import (
	"testing"
	"time"
)

func TestResolveTZ(t *testing.T) {
	if loc, err := resolveTZ("utc"); err != nil || loc != time.UTC {
		t.Errorf("utc → %v, %v", loc, err)
	}
	if loc, err := resolveTZ(""); err != nil || loc != time.Local {
		t.Errorf("empty → %v, %v (want Local)", loc, err)
	}
	if loc, err := resolveTZ("local"); err != nil || loc != time.Local {
		t.Errorf("local → %v, %v (want Local)", loc, err)
	}
	if loc, err := resolveTZ("Asia/Tokyo"); err != nil || loc.String() != "Asia/Tokyo" {
		t.Errorf("Asia/Tokyo → %v, %v", loc, err)
	}
	if _, err := resolveTZ("Nowhere/Bogus"); err == nil {
		t.Error("bogus tz should error")
	}
}

func TestParseSince_Timezone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("tzdata unavailable:", err)
	}
	// A bare date is midnight in the given zone.
	u, err := parseSince("2026-07-05", tokyo)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 5, 0, 0, 0, 0, tokyo).Unix()
	if u != want {
		t.Errorf("since 2026-07-05 JST = %d, want %d", u, want)
	}
	// UTC midnight of the same date is 9 hours later than JST midnight.
	uu, _ := parseSince("2026-07-05", time.UTC)
	if uu-u != 9*3600 {
		t.Errorf("UTC−JST midnight = %ds, want %d", uu-u, 9*3600)
	}
}

func TestParseSince_TodayInZone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("tzdata unavailable:", err)
	}
	u, err := parseSince("today", tokyo)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(tokyo)
	want := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tokyo).Unix()
	if u != want {
		t.Errorf("today JST = %d, want local midnight %d", u, want)
	}
}
