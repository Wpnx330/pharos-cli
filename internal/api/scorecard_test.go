package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scorecardHitJSON matches the registry's search-hit scorecard summary.
const scorecardHitJSON = `{
  "name": "scored-hit",
  "version": "1.0.0",
  "score": 0.9,
  "scorecard": {"score": 77, "grade": "B"}
}`

// scorecardNullHitJSON matches the registry's explicit-null contract for
// unscored and federated packages.
const scorecardNullHitJSON = `{
  "name": "unscored-hit",
  "version": "1.0.0",
  "score": 0.9,
  "scorecard": null
}`

// scorecardMissingHitJSON matches older registries that omit the key.
const scorecardMissingHitJSON = `{
  "name": "legacy-hit",
  "version": "1.0.0",
  "score": 0.9
}`

func TestSearchResultUnmarshalScorecardSummary(t *testing.T) {
	var got SearchResult
	if err := json.Unmarshal([]byte(scorecardHitJSON), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Scorecard == nil {
		t.Fatal("Scorecard = nil, want parsed")
	}
	if got.Scorecard.Score != 77 || got.Scorecard.Grade != "B" {
		t.Errorf("Scorecard = %d/%s, want 77/B", got.Scorecard.Score, got.Scorecard.Grade)
	}
}

func TestSearchResultUnmarshalScorecardNullIsNil(t *testing.T) {
	var got SearchResult
	if err := json.Unmarshal([]byte(scorecardNullHitJSON), &got); err != nil {
		t.Fatalf("unmarshal null scorecard: %v", err)
	}
	if got.Scorecard != nil {
		t.Errorf("Scorecard = %+v, want nil for explicit null", got.Scorecard)
	}
}

func TestSearchResultUnmarshalScorecardAbsentIsNil(t *testing.T) {
	var got SearchResult
	if err := json.Unmarshal([]byte(scorecardMissingHitJSON), &got); err != nil {
		t.Fatalf("unmarshal missing scorecard: %v", err)
	}
	if got.Scorecard != nil {
		t.Errorf("Scorecard = %+v, want nil when key absent", got.Scorecard)
	}
}

func TestSearchResultMarshalScorecardEchoesRegistryContract(t *testing.T) {
	// The CLI's --json echo must reproduce the registry's explicit-null
	// contract: nil marshals as "scorecard": null (no omitempty), scored
	// rows carry the {score, grade} object.
	var got SearchResult
	if err := json.Unmarshal([]byte(scorecardNullHitJSON), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"scorecard"`) {
		t.Errorf("marshalled hit %s lost the scorecard key (explicit null required)", out)
	}
}

func TestPackageDetailUnmarshalScorecard(t *testing.T) {
	raw := `{
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
				{"name": "Identity", "weight": 20, "points": 20, "partial": false, "detail": "dns proof"},
				{"name": "Adoption", "weight": 25, "points": 12, "partial": true, "detail": "single version"}
			]
		}
	}`
	var got PackageDetail
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Scorecard == nil {
		t.Fatal("Scorecard = nil, want parsed")
	}
	if got.Scorecard.Score != 77 || got.Scorecard.Grade != "B" || got.Scorecard.Heuristic != "heuristic-v1" {
		t.Errorf("Scorecard header = %d/%s/%q, want 77/B/heuristic-v1",
			got.Scorecard.Score, got.Scorecard.Grade, got.Scorecard.Heuristic)
	}
	if got.Scorecard.KnownWeight != 100 {
		t.Errorf("KnownWeight = %d, want 100", got.Scorecard.KnownWeight)
	}
	if len(got.Scorecard.Components) != 2 {
		t.Fatalf("Components = %d, want 2", len(got.Scorecard.Components))
	}
	partial := got.Scorecard.Components[1]
	if !partial.Partial || partial.Points != 12 {
		t.Errorf("Adoption component = %+v, want partial with 12 points", partial)
	}
}

func TestPackageDetailUnmarshalScorecardAbsentIsNil(t *testing.T) {
	raw := `{"name": "plain-pkg", "versions": [], "dist_tags": {}}`
	var got PackageDetail
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Scorecard != nil {
		t.Errorf("Scorecard = %+v, want nil when key absent", got.Scorecard)
	}
}

func TestGetScorecardScored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/packages/graded/scorecard" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"scored": true,
			"name": "graded",
			"score": 77,
			"grade": "B",
			"heuristic": "heuristic-v1",
			"scoredAt": "2026-09-06T12:00:00Z",
			"knownWeight": 100,
			"components": [{"name": "Identity", "weight": 20, "points": 20, "partial": false, "detail": "dns"}]
		}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "")
	got, err := c.GetScorecard("graded")
	if err != nil {
		t.Fatalf("GetScorecard: %v", err)
	}
	if !got.Scored || got.Reason != "" {
		t.Errorf("Scored/Reason = %v/%q, want true/empty", got.Scored, got.Reason)
	}
	if got.Score == nil || *got.Score != 77 || got.Grade != "B" {
		t.Errorf("Score/Grade = %v/%q, want 77/B", got.Score, got.Grade)
	}
	if got.KnownWeight != 100 || got.Heuristic != "heuristic-v1" || got.ScoredAt == "" {
		t.Errorf("meta = %d/%q/%q, want 100/heuristic-v1/timestamp", got.KnownWeight, got.Heuristic, got.ScoredAt)
	}
	if len(got.Components) != 1 || got.Components[0].Name != "Identity" {
		t.Errorf("Components = %#v, want one Identity", got.Components)
	}
}

func TestGetScorecardNotScoredIsNormal200(t *testing.T) {
	for _, tt := range []struct {
		name   string
		reason string
	}{
		{name: "pending", reason: "pending"},
		{name: "federated", reason: "federated"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"scored":false,"reason":"` + tt.reason + `"}`))
			}))
			t.Cleanup(srv.Close)

			c := New(srv.URL, "")
			got, err := c.GetScorecard("some-pkg")
			if err != nil {
				t.Fatalf("GetScorecard: %v", err)
			}
			if got.Scored || got.Reason != tt.reason {
				t.Errorf("Scored/Reason = %v/%q, want false/%q", got.Scored, got.Reason, tt.reason)
			}
			if got.Score != nil || got.Grade != "" {
				t.Errorf("Score/Grade = %v/%q, want nil/empty when not scored", got.Score, got.Grade)
			}
		})
	}
}

func TestGetScorecardScopedNamePath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scored":false,"reason":"pending"}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "")
	if _, err := c.GetScorecard("io.github.acme/tool"); err != nil {
		t.Fatalf("GetScorecard: %v", err)
	}
	// Scoped names hit the scoped route with the @ prefix restored.
	if !strings.HasPrefix(gotPath, "/v1/packages/@io.github.acme/tool/scorecard") {
		t.Errorf("path = %q, want scoped scorecard route", gotPath)
	}
}

func TestGetScorecardHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"INTERNAL"}`))
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "")
	if _, err := c.GetScorecard("some-pkg"); err == nil {
		t.Fatal("GetScorecard on 500 should error")
	}
}
