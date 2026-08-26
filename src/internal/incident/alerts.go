// SPDX-License-Identifier: Apache-2.0
package incident

// Alertmanager integration (task 049): when a fault declares expectAlert,
// `incident status` also asks Alertmanager whether the page actually fired —
// closing the loop from fault to alert to fix.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AlertStatus reports whether the fault's expected alert is firing.
type AlertStatus struct {
	Expected string    `json:"expected"`
	Fired    bool      `json:"fired"`
	Since    time.Time `json:"since,omitempty"`
	Error    string    `json:"error,omitempty"` // query problems are reported, never fatal
}

// queryAlert asks the Alertmanager v2 API for an active alert by name.
func queryAlert(ctx context.Context, client *http.Client, amURL, alertname string) *AlertStatus {
	st := &AlertStatus{Expected: alertname}
	if amURL == "" {
		st.Error = "no Alertmanager URL configured (set ALERTMANAGER_URL)"
		return st
	}

	endpoint := strings.TrimSuffix(amURL, "/") + "/api/v2/alerts?filter=" +
		url.QueryEscape(fmt.Sprintf("alertname=%q", alertname))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	resp, err := client.Do(req)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		st.Error = fmt.Sprintf("alertmanager returned status %d", resp.StatusCode)
		return st
	}

	var alerts []struct {
		StartsAt time.Time `json:"startsAt"`
		Status   struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&alerts); err != nil {
		st.Error = fmt.Sprintf("decoding alertmanager response: %v", err)
		return st
	}

	for _, a := range alerts {
		if a.Status.State == "active" {
			st.Fired = true
			if st.Since.IsZero() || a.StartsAt.Before(st.Since) {
				st.Since = a.StartsAt
			}
		}
	}
	return st
}
