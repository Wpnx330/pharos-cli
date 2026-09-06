package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

func TestAuditScorecardCell(t *testing.T) {
	tests := []struct {
		name      string
		scorecard *api.ScorecardSummary
		want      string
	}{
		{name: "nil is a dash", scorecard: nil, want: listDash},
		{name: "scored", scorecard: &api.ScorecardSummary{Score: 77, Grade: "B"}, want: "77 (B)"},
		{name: "zero score", scorecard: &api.ScorecardSummary{Score: 0, Grade: "F"}, want: "0 (F)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := auditScorecardCell(tt.scorecard)
			if !strings.Contains(got, tt.want) {
				t.Errorf("auditScorecardCell(%v) = %q, want it to contain %q", tt.scorecard, got, tt.want)
			}
		})
	}
}

func TestAuditScorecardCellGradeColours(t *testing.T) {
	// The band colouring must not change the visible text; it only wraps it.
	sc := &api.ScorecardSummary{Score: 92, Grade: "A"}
	if got := auditScorecardCell(sc); !strings.Contains(got, "92 (A)") {
		t.Errorf("A-grade cell = %q, want it to contain \"92 (A)\"", got)
	}
	if got := auditScorecardCell(nil); !strings.Contains(got, listDash) {
		t.Errorf("nil cell = %q, want dash", got)
	}
	_ = ui.Muted // keep ui import stable if colours are compiled out
}

func TestFormatAuditReportShowsSecurityColumn(t *testing.T) {
	report := &auditReport{
		Total:   2,
		Scanned: 2,
		Entries: []auditEntry{
			{Server: "graded-server", Version: "1.0.0", Scorecard: &api.ScorecardSummary{Score: 77, Grade: "B"}},
			{Server: "ungraded-server", Version: "2.0.0"},
		},
	}
	out := formatAuditReport(report)
	if !strings.Contains(out, "SECURITY") {
		t.Errorf("audit table missing SECURITY header:\n%s", out)
	}
	if !strings.Contains(out, "77 (B)") {
		t.Errorf("audit table missing scored cell:\n%s", out)
	}
}

func TestRunAuditFetchesScorecards(t *testing.T) {
	var scorecardNames []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/advisories/"):
			_, _ = w.Write([]byte(`[]`))
		case strings.HasPrefix(r.URL.Path, "/v1/packages/") && strings.HasSuffix(r.URL.Path, "/scorecard"):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/packages/"), "/scorecard")
			scorecardNames = append(scorecardNames, name)
			if name == "graded-server" {
				_, _ = w.Write([]byte(`{"scored":true,"name":"graded-server","score":77,"grade":"B"}`))
			} else {
				_, _ = w.Write([]byte(`{"scored":false,"reason":"federated"}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := api.New(srv.URL, "")
	report := runAudit(client, []serverInfo{
		{Name: "graded-server", Version: "1.0.0"},
		{Name: "federated-server", Version: "2.0.0"},
	})

	if len(scorecardNames) != 2 {
		t.Fatalf("registry saw %d scorecard lookups (%v), want 2", len(scorecardNames), scorecardNames)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(report.Entries))
	}
	if report.Entries[0].Scorecard == nil || report.Entries[0].Scorecard.Score != 77 || report.Entries[0].Scorecard.Grade != "B" {
		t.Errorf("graded entry scorecard = %+v, want 77/B", report.Entries[0].Scorecard)
	}
	if report.Entries[1].Scorecard != nil {
		t.Errorf("federated entry scorecard = %+v, want nil (scored=false)", report.Entries[1].Scorecard)
	}
	if report.HasVulns {
		t.Error("HasVulns = true, want false (scorecard is advisory-only)")
	}
}

func TestRunAuditScorecardFailureIsNonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/advisories/"):
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/scorecard"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"INTERNAL"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := api.New(srv.URL, "")
	report := runAudit(client, []serverInfo{{Name: "flaky-server", Version: "1.0.0"}})

	if report.Scanned != 1 || len(report.Entries) != 1 {
		t.Fatalf("report = scanned %d, %d entries; want 1/1 (scorecard failure must not abort)", report.Scanned, len(report.Entries))
	}
	if report.Entries[0].Scorecard != nil {
		t.Errorf("scorecard = %+v, want nil after lookup failure", report.Entries[0].Scorecard)
	}
	if report.Entries[0].Error != "" {
		t.Errorf("entry error = %q, want empty (scorecard failure is not an audit error)", report.Entries[0].Error)
	}
}
