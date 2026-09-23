package timeutil

import "testing"

func TestUTCInstantAndIndonesianZone(t *testing.T) {
	instant, err := ParseInstant("2026-09-23T12:00:00+08:00")
	if err != nil {
		t.Fatal(err)
	}
	if got := instant.Format("2006-01-02T15:04:05Z07:00"); got != "2026-09-23T04:00:00Z" {
		t.Fatalf("UTC = %s", got)
	}
	jakarta, err := InZone(instant, "Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	if got := jakarta.Format("15:04"); got != "11:00" {
		t.Fatalf("Jakarta = %s", got)
	}
}
