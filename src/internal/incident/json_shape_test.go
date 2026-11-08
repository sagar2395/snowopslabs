package incident

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// AlertStatus and Record are returned by the API. A zero time must leave its
// key out, so a reader never sees a year-1 timestamp for "not set".
func TestJSONOmitsZeroTimes(t *testing.T) {
	set := time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		value   any
		key     string
		wantKey bool
	}{
		{name: "alert since zero is omitted", value: AlertStatus{Expected: "LabFault"}, key: `"since"`, wantKey: false},
		{name: "alert since set is sent", value: AlertStatus{Expected: "LabFault", Since: set}, key: `"since"`, wantKey: true},
		{name: "record firstCheckedAt zero is omitted", value: Record{Fault: "fault-a"}, key: `"firstCheckedAt"`, wantKey: false},
		{name: "record firstCheckedAt set is sent", value: Record{Fault: "fault-a", FirstCheckedAt: set}, key: `"firstCheckedAt"`, wantKey: true},
		{name: "record resolvedAt is always sent", value: Record{Fault: "fault-a"}, key: `"resolvedAt"`, wantKey: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(b), tt.key); got != tt.wantKey {
				t.Errorf("JSON = %s, want %s present=%v", b, tt.key, tt.wantKey)
			}
		})
	}
}
