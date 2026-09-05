package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPasswordChangeSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	st, err := New(path, "original-password")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetAdminPassword("changed-password"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = New(path, "original-password")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if !st.VerifyAdminPassword("changed-password") || st.VerifyAdminPassword("original-password") {
		t.Fatal("restart restored old password")
	}
}

func TestBillingCycleShortMonths(t *testing.T) {
	for _, tc := range []struct {
		now, start, end string
		reset           int
	}{
		{"2026-02-28T12:00:00Z", "2026-02-28", "2026-03-31", 31},
		{"2024-02-29T00:00:00Z", "2024-02-29", "2024-03-31", 31},
		{"2026-04-30T12:00:00Z", "2026-04-30", "2026-05-31", 31},
		{"2026-02-27T23:59:59Z", "2026-01-31", "2026-02-28", 31},
		{"2026-02-28T00:00:00Z", "2026-02-28", "2026-03-30", 30},
		{"2026-12-31T12:00:00Z", "2026-12-31", "2027-01-31", 31},
	} {
		t.Run(tc.now, func(t *testing.T) {
			now, _ := time.Parse(time.RFC3339, tc.now)
			start, end := GetBillingCycleRange(tc.reset, now)
			if start.Format("2006-01-02") != tc.start || end.Format("2006-01-02") != tc.end || start.After(now) || !end.After(now) {
				t.Fatalf("wrong range %s to %s", start, end)
			}
			n := Node{ResetDay: tc.reset, CycleStart: start.AddDate(0, 0, -1), InitialUsed: 10, CurrentCycleUsed: 20}
			n.checkCycleRollover(now)
			if n.InitialUsed != 0 || n.CurrentCycleUsed != 0 || !n.CycleStart.Equal(start) {
				t.Fatal("cycle did not reset")
			}
			n.CurrentCycleUsed = 5
			n.checkCycleRollover(now)
			if n.CurrentCycleUsed != 5 {
				t.Fatal("same cycle reset twice")
			}
		})
	}
}
