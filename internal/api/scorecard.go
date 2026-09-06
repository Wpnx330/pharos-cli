package api

import "encoding/json"

// ScorecardSummary is the security scorecard summary carried on search
// hits: {"score": 77, "grade": "B"}. Registry sends null (and older
// registries omit the key) for unscored and federated packages; both
// deserialize to a nil pointer.
type ScorecardSummary struct {
	Score int    `json:"score"`
	Grade string `json:"grade"`
}

// ScorecardComponent is one scored dimension of the heuristic, mirroring
// the registry's scorecard component shape.
type ScorecardComponent struct {
	Name    string `json:"name"`
	Weight  int    `json:"weight"`
	Points  int    `json:"points"`
	Partial bool   `json:"partial"`
	Detail  string `json:"detail"`
}

// ScorecardDetail is the full security scorecard attached to package
// detail responses when the package has been scored.
type ScorecardDetail struct {
	Score       int                  `json:"score"`
	Grade       string               `json:"grade"`
	Heuristic   string               `json:"heuristic"`
	ScoredAt    string               `json:"scoredAt"`
	KnownWeight int                  `json:"knownWeight"`
	Components  []ScorecardComponent `json:"components,omitempty"`
}

// ScorecardResult is the response of GET /v1/packages/{name}/scorecard.
// Scored=false carries Reason ("pending" or "federated") and no grade —
// not-scored is a normal 200, never an error.
type ScorecardResult struct {
	Scored      bool                 `json:"scored"`
	Reason      string               `json:"reason,omitempty"`
	Name        string               `json:"name,omitempty"`
	Score       *int                 `json:"score,omitempty"`
	Grade       string               `json:"grade,omitempty"`
	Heuristic   string               `json:"heuristic,omitempty"`
	ScoredAt    string               `json:"scoredAt,omitempty"`
	KnownWeight int                  `json:"knownWeight,omitempty"`
	Components  []ScorecardComponent `json:"components,omitempty"`
}

// GetScorecard fetches the security scorecard for the named package.
// Not-scored states come back as a 200 with Scored=false so callers can
// render "not scored" uniformly; only transport/HTTP failures error.
func (c *Client) GetScorecard(name string) (*ScorecardResult, error) {
	data, err := c.get("/v1/packages/" + packagePath(name) + "/scorecard")
	if err != nil {
		return nil, err
	}
	var res ScorecardResult
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
