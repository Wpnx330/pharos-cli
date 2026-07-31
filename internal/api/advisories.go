package api

import "encoding/json"

// Advisory represents a single security advisory for a package.
type Advisory struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	PackageName string `json:"package_name"`
	Affected    string `json:"affected_range"`
	FixedIn     string `json:"fixed_version"`
	CVE         string `json:"cve,omitempty"`
	URL         string `json:"url,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

// GetAdvisories fetches security advisories for the named package.
func (c *Client) GetAdvisories(name string) ([]Advisory, error) {
	data, err := c.get("/v1/advisories/" + packagePath(name))
	if err != nil {
		return nil, err
	}
	var advisories []Advisory
	if err := json.Unmarshal(data, &advisories); err != nil {
		return nil, err
	}
	return advisories, nil
}
