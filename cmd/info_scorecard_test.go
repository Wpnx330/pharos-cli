package cmd

import (
	"encoding/json"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
)

func TestFormatInfoScorecard(t *testing.T) {
	tests := []struct {
		name      string
		scorecard *api.ScorecardDetail
		want      string
	}{
		{name: "nil is not scored", scorecard: nil, want: "Not scored"},
		{name: "scored", scorecard: &api.ScorecardDetail{Score: 77, Grade: "B"}, want: "77/100 (B)"},
		{name: "top grade", scorecard: &api.ScorecardDetail{Score: 92, Grade: "A"}, want: "92/100 (A)"},
		{name: "failing grade", scorecard: &api.ScorecardDetail{Score: 30, Grade: "F"}, want: "30/100 (F)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatInfoScorecard(tt.scorecard); got != tt.want {
				t.Errorf("formatInfoScorecard(%v) = %q, want %q", tt.scorecard, got, tt.want)
			}
		})
	}
}

func TestPackageDetailJSONRoundTripsScorecard(t *testing.T) {
	// info --json marshals PackageDetail verbatim; the scorecard must
	// survive the round trip with its registry shape intact.
	in := `{
		"name": "scored-pkg",
		"versions": [],
		"dist_tags": {"latest": "1.0.0"},
		"scorecard": {
			"score": 77,
			"grade": "B",
			"heuristic": "heuristic-v1",
			"scoredAt": "2026-09-06T00:00:00Z",
			"knownWeight": 100,
			"components": [
				{"name": "Identity", "weight": 20, "points": 20, "partial": false, "detail": "dns proof"}
			]
		}
	}`
	pkg, err := unmarshalPackageDetailForTest([]byte(in))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pkg.Scorecard == nil {
		t.Fatal("Scorecard is nil, want parsed")
	}
	if pkg.Scorecard.Score != 77 || pkg.Scorecard.Grade != "B" {
		t.Errorf("Scorecard = %d/%s, want 77/B", pkg.Scorecard.Score, pkg.Scorecard.Grade)
	}
	if pkg.Scorecard.KnownWeight != 100 || pkg.Scorecard.Heuristic != "heuristic-v1" {
		t.Errorf("Scorecard meta = %d/%q, want 100/heuristic-v1", pkg.Scorecard.KnownWeight, pkg.Scorecard.Heuristic)
	}
	if len(pkg.Scorecard.Components) != 1 || pkg.Scorecard.Components[0].Name != "Identity" {
		t.Fatalf("Components = %#v, want one Identity component", pkg.Scorecard.Components)
	}
}

func TestPackageDetailJSONWithoutScorecardStaysNil(t *testing.T) {
	in := `{"name": "plain-pkg", "versions": [], "dist_tags": {}}`
	pkg, err := unmarshalPackageDetailForTest([]byte(in))
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if pkg.Scorecard != nil {
		t.Errorf("Scorecard = %+v, want nil when key absent", pkg.Scorecard)
	}
}

// unmarshalPackageDetailForTest parses a package detail payload the same
// way the info command's API client does.
func unmarshalPackageDetailForTest(data []byte) (*api.PackageDetail, error) {
	var pd api.PackageDetail
	if err := json.Unmarshal(data, &pd); err != nil {
		return nil, err
	}
	return &pd, nil
}
