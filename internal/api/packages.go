package api

import "encoding/json"

// VersionInfo describes a single published version of a package.
type VersionInfo struct {
	Version   string `json:"version"`
	CreatedAt string `json:"created_at"`
	Status    string `json:"status"`
	Deprecated bool  `json:"deprecated"`
}

// PackageDetail is the full package metadata returned by the package
// detail endpoint.
type PackageDetail struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	License     string            `json:"license"`
	RepoURL     string            `json:"repo_url"`
	RepoSource  string            `json:"repo_source"`
	ReadmeURL   string            `json:"readme_url"`
	CreatedAt   string            `json:"created_at"`
	ModifiedAt  string            `json:"modified_at"`
	DistTags    map[string]string `json:"dist_tags"`
	Versions    []VersionDetail   `json:"versions"`
}

// VersionDetail is a version entry embedded in PackageDetail.
type VersionDetail struct {
	Version     string   `json:"version"`
	Manifest    Manifest `json:"manifest"`
	Deprecated  bool     `json:"deprecated"`
	Status      string   `json:"status"`
	CreatedAt   string   `json:"created_at"`
}

// Manifest is the package manifest embedded in each version.
type Manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Transport   string   `json:"transport"`
	Description string   `json:"description"`
	Capabilities []string `json:"capabilities"`
}

// GetPackage fetches full details for the named package.
func (c *Client) GetPackage(name string) (*PackageDetail, error) {
	data, err := c.get("/v1/packages/" + encodeQuery(name))
	if err != nil {
		return nil, err
	}
	var pd PackageDetail
	if err := json.Unmarshal(data, &pd); err != nil {
		return nil, err
	}
	return &pd, nil
}

// GetVersions fetches the version list for the named package.
func (c *Client) GetVersions(name string) ([]VersionInfo, error) {
	data, err := c.get("/v1/packages/" + encodeQuery(name) + "/versions")
	if err != nil {
		return nil, err
	}
	var versions []VersionInfo
	if err := json.Unmarshal(data, &versions); err != nil {
		return nil, err
	}
	return versions, nil
}

// GetDistTags fetches the dist-tags map for the named package.
func (c *Client) GetDistTags(name string) (map[string]string, error) {
	data, err := c.get("/v1/packages/" + encodeQuery(name) + "/dist-tags")
	if err != nil {
		return nil, err
	}
	var tags map[string]string
	if err := json.Unmarshal(data, &tags); err != nil {
		return nil, err
	}
	return tags, nil
}
