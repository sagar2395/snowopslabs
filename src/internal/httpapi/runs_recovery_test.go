// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"github.com/snowops-oss/snowopslabs/internal/runowner"
	"github.com/snowops-oss/snowopslabs/internal/store"
)

func TestRunRecoveryAPI(t *testing.T) {
	for _, released := range []bool{false, true} {
		t.Run(map[bool]string{false: "required", true: "released"}[released], func(t *testing.T) {
			s, st := runsTestServer(t)
			owner, err := runowner.New(t.Context(), st.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			if err := st.CreateRun(t.Context(), store.Run{ID: "interrupted", Kind: "lab.up", LockKey: "lab", OwnerID: owner.ID}); err != nil {
				t.Fatal(err)
			}
			if err := st.StartOwnedRun(t.Context(), "interrupted", owner.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := st.AppendLogs(t.Context(), "interrupted", []store.LogLine{{Text: "saved output"}}); err != nil {
				t.Fatal(err)
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := st.RecoverOrphanedRuns(t.Context(), time.Now()); err != nil {
				t.Fatal(err)
			}
			state := "required"
			if released {
				if _, err := st.ReleaseRecoveredRun(t.Context(), "interrupted", "uid=123 (operator)"); err != nil {
					t.Fatal(err)
				}
				state = "released"
			}
			for _, endpoint := range []string{"list", "detail", "logs"} {
				w := httptest.NewRecorder()
				req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/v2/runs/interrupted", nil), map[string]string{"id": "interrupted"})
				switch endpoint {
				case "list":
					s.handleRunsList(w, req)
				case "detail":
					s.handleRunGet(w, req)
				case "logs":
					s.handleRunLogs(w, req)
				}
				var got map[string]any
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
					t.Fatalf("%s response = %d: %s", endpoint, w.Code, w.Body.String())
				}
				if endpoint == "list" {
					got = got["items"].([]any)[0].(map[string]any)
				}
				reason, reasonPresent := got["recoveryReason"].(string)
				if got["recoveryState"] != state || !reasonPresent || reason == "" || got["ownerId"] != owner.ID {
					t.Errorf("%s omitted recovery: %s", endpoint, w.Body.String())
				}
				if released && (got["releasedBy"] != "uid=123 (operator)" || got["releasedAt"] == nil) {
					t.Errorf("%s omitted release audit: %s", endpoint, w.Body.String())
				}
				if !released && got["recoveryCommand"] != "labctl runs recover interrupted --force" {
					t.Errorf("%s omitted recovery command: %s", endpoint, w.Body.String())
				}
			}
			logs, _ := st.ReadLogs(t.Context(), "interrupted", 0, 100)
			cursor := logs[len(logs)-1].Seq
			w := httptest.NewRecorder()
			req := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/api/v2/runs/interrupted/logs?after="+strconv.FormatInt(cursor, 10), nil), map[string]string{"id": "interrupted"})
			s.handleRunLogs(w, req)
			var got runLogsView
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Done != released || len(got.Lines) != 0 {
				t.Errorf("completion contract changed: %+v", got)
			}
			if !released {
				w = httptest.NewRecorder()
				s.handleRunCancel(w, req)
				if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "runs recover") {
					t.Errorf("cancel must explain recovery, got %d: %s", w.Code, w.Body.String())
				}
				after, _ := st.ReadLogs(t.Context(), "interrupted", 0, 100)
				if len(after) != len(logs) {
					t.Fatal("cancel modified recovery logs")
				}
			}
		})
	}
}
