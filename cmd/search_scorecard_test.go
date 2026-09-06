package cmd

import (
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

func TestFormatSearchScorecard(t *testing.T) {
	tests := []struct {
		name      string
		scorecard *api.ScorecardSummary
		want      string
	}{
		{name: "nil renders dash", scorecard: nil, want: listDash},
		{name: "scored renders score and grade", scorecard: &api.ScorecardSummary{Score: 77, Grade: "B"}, want: "77 (B)"},
		{name: "top grade", scorecard: &api.ScorecardSummary{Score: 92, Grade: "A"}, want: "92 (A)"},
		{name: "failing grade", scorecard: &api.ScorecardSummary{Score: 30, Grade: "F"}, want: "30 (F)"},
		{name: "zero score still renders", scorecard: &api.ScorecardSummary{Score: 0, Grade: "F"}, want: "0 (F)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatSearchScorecard(tt.scorecard); got != tt.want {
				t.Errorf("formatSearchScorecard(%v) = %q, want %q", tt.scorecard, got, tt.want)
			}
		})
	}
}

func TestSearchTableRowScorecardCell(t *testing.T) {
	row := searchTableRow(api.SearchResult{
		Name:      "scored-hit",
		Version:   "1.0.0",
		Scorecard: &api.ScorecardSummary{Score: 77, Grade: "B"},
	})
	if row[6] != "77 (B)" {
		t.Errorf("SECURITY = %q, want \"77 (B)\"", row[6])
	}
}

func TestSearchTableRowUnscoredUsesDash(t *testing.T) {
	row := searchTableRow(api.SearchResult{Name: "legacy-hit", Version: "1.2.3"})
	if row[6] != listDash {
		t.Errorf("SECURITY = %q, want %q (nil scorecard)", row[6], listDash)
	}
}

func TestSearchTableRendersSecurityColumn(t *testing.T) {
	// Integration through the real renderer: the SECURITY header and the
	// scored cell survive rendering, and MaxWidth truncation keeps the
	// table honest.
	r := api.SearchResult{
		Name:      "secure-thing",
		Version:   "1.0.0",
		Scorecard: &api.ScorecardSummary{Score: 77, Grade: "B"},
	}
	out := ui.RenderTable(searchTableColumns(), []ui.TableRow{searchTableRow(r)})
	if !strings.Contains(out, "SECURITY") {
		t.Errorf("rendered table missing SECURITY header:\n%s", out)
	}
	if !strings.Contains(out, "77 (B)") {
		t.Errorf("rendered table missing scorecard cell:\n%s", out)
	}
}
