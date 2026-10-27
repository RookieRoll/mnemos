package memory_test

import (
	"fmt"
	"testing"
	"time"
)

func TestNearDuplicatePreservesTTL(t *testing.T) {
	for _, tc := range []struct {
		before, after int
		advance       time.Duration
		merged        bool
	}{
		{1, 0, 0, false}, {0, 1, 0, false}, {1, 2, 0, false},
		{1, 1, 0, true}, {1, 1, time.Hour, false},
	} {
		for _, sameSession := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-to-%d/%s/session=%v", tc.before, tc.after, tc.advance, sameSession), func(t *testing.T) {
				f := newNearDupFixture(t, nil)
				in := nearDupNote("Use WAL.")
				in.TTLDays = tc.before
				if sameSession {
					f.createSession(t, "ttl-session")
					in.SessionID = "ttl-session"
				}
				first := f.save(t, in)
				f.advance(tc.advance)
				in.TTLDays = tc.after
				in.Content = "Use WAL!"
				if sameSession {
					in.Content = "Use WAL for this project"
				} else {
					in.Title = "new lifetime requirement"
				}
				second := f.save(t, in)
				if (first.Observation.ID == second.Observation.ID) != tc.merged {
					t.Fatalf("unexpected fold: %+v", second)
				}
				if tc.after == 0 {
					if second.Observation.ExpiresAt != nil {
						t.Fatal("permanent save became temporary")
					}
				} else if want := f.now.AddDate(0, 0, tc.after); second.Observation.ExpiresAt == nil || !second.Observation.ExpiresAt.Equal(want) {
					t.Fatalf("expiry=%v want=%v", second.Observation.ExpiresAt, want)
				}
			})
		}
	}
}
