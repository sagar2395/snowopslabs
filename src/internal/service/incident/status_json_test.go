package incident

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Status is returned by the API. An incident with no run yet has a zero Since,
// and the JSON must leave the key out.
func TestStatus_JSONOmitsZeroSince(t *testing.T) {
	tests := []struct {
		name      string
		since     time.Time
		wantKey   bool
		wantValue string
	}{
		{name: "zero since is omitted", wantKey: false},
		{name: "set since is sent", since: time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC), wantKey: true, wantValue: `"since":"2026-11-07T09:00:00Z"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(Status{Fault: "fault-a", Since: tt.since})
			if err != nil {
				t.Fatal(err)
			}
			got := string(b)
			if hasKey := strings.Contains(got, `"since"`); hasKey != tt.wantKey {
				t.Errorf("status JSON = %s, want key present=%v", got, tt.wantKey)
			}
			if tt.wantValue != "" && !strings.Contains(got, tt.wantValue) {
				t.Errorf("status JSON = %s, want %s", got, tt.wantValue)
			}
		})
	}
}
