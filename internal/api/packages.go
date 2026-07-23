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
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Transport    string            `json:"transport"`
	Description  string            `json:"description"`
	Capabilities []string          `json:"capabilities"`
	// Runtime hint for stdio servers: "npx", "uvx", "docker", "binary".
	Runtime string `json:"runtime,omitempty"`
	// Package is the npm/pip/docker image name to pass to the runtime.
	// e.g. "@modelcontextprotocol/server-git" for npx, "mcp-server-git" for uvx.
	Package string `json:"package,omitempty"`
	// Bin is the relative path to the executable inside the tarball
	// (for "binary" runtime). If empty, runtime package is used directly.
	Bin string `json:"bin,omitempty"`
	// Endpoint is the URL for http/sse transport servers.
	Endpoint string `json:"endpoint,omitempty"`
	// Env is the environment variables to set in the client config.
	Env map[string]string `json:"env,omitempty"`
	// Integrity is the sha512 hash of the tarball ("sha512-<base64>").
	Integrity string `json:"integrity,omitempty"`
	// Args are extra arguments appended after the package name.
	Args []string `json:"args,omitempty"`
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

// GetVersionManifest fetches the manifest for a single version.
func (c *Client) GetVersionManifest(name, version string) (*VersionDetail, error) {
	data, err := c.get("/v1/packages/" + encodeQuery(name) + "/versions/" + encodeQuery(version))
	if err != nil {
		return nil, err
	}
	var vd VersionDetail
	if err := json.Unmarshal(data, &vd); err != nil {
		return nil, err
	}
	return &vd, nil
}

// VersionStrings extracts the list of version strings from a PackageDetail.
func (pd *PackageDetail) VersionStrings() []string {
	versions := make([]string, len(pd.Versions))
	for i, v := range pd.Versions {
		versions[i] = v.Version
	}
	return versions
}

// FindVersion returns the VersionDetail for the given version string,
// or nil if not found.
func (pd *PackageDetail) FindVersion(version string) *VersionDetail {
	for i := range pd.Versions {
		if pd.Versions[i].Version == version {
			return &pd.Versions[i]
		}
	}
	return nil
}

// TarballURL constructs the registry tarball URL for a name@version.
func (c *Client) TarballURL(name, version string) string {
	return c.BaseURL + "/v1/tarballs/" + name + "/" + version
}
