package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// HealthResponse is the response from the /v1/health endpoint.
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// Health checks the registry health endpoint.
func (c *Client) Health() (*HealthResponse, error) {
	data, err := c.get("/v1/health")
	if err != nil {
		return nil, err
	}
	var h HealthResponse
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// UploadResponse is the response from the POST /v1/uploads endpoint.
type UploadResponse struct {
	UploadID    string `json:"uploadId"`
	URL         string `json:"url"`
	ContentHash string `json:"contentHash"`
}

// Upload initiates an upload by posting the package name, version, and
// tarball size to the registry. The returned UploadResponse contains a
// presigned URL the client should PUT the tarball bytes to.
func (c *Client) Upload(name, version string, tarballSize int64) (*UploadResponse, error) {
	payload := map[string]any{
		"name":    name,
		"version": version,
		"size":    tarballSize,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	data, _, err := c.do(http.MethodPost, "/v1/uploads", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	var resp UploadResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UploadToPresigned PUTs the tarball bytes to the presigned URL returned
// by the upload session. This bypasses the registry API and goes directly
// to blob storage (S3/MinIO/R2).
func (c *Client) UploadToPresigned(presignedURL string, tarball []byte) error {
	req, err := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(tarball))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/gzip")
	req.ContentLength = int64(len(tarball))

	// Use a separate HTTP client without the auth header — presigned URLs
	// are authenticated via the URL signature, not Bearer tokens.
	client := &http.Client{}
	if c.HTTPClient != nil {
		client = c.HTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("upload to blob store failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("blob store returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// PublishRequest is the body sent to the PUT /v1/packages/<name> endpoint.
type PublishRequest struct {
	Version           string          `json:"version"`
	Manifest          json.RawMessage `json:"manifest"`
	BlobRef           string          `json:"blobRef"`
	ArtifactType      string          `json:"artifactType,omitempty"`
	ArtifactSize      int64           `json:"artifactSize,omitempty"`
	ArtifactIntegrity string          `json:"artifactIntegrity,omitempty"`
}

// Publish registers a new package version with the registry.
func (c *Client) Publish(name string, req *PublishRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, _, err = c.do(http.MethodPut, "/v1/packages/"+packagePath(name), bytes.NewReader(body))
	return err
}

// CreatePackageRequest is the body for POST /v1/packages.
type CreatePackageRequest struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Description string `json:"description"`
}

// CreatePackage creates a new package within the user's namespace.
// This must be called before publishing the first version.
func (c *Client) CreatePackage(req *CreatePackageRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data, statusCode, err := c.do(http.MethodPost, "/v1/packages", bytes.NewReader(body))
	if err != nil {
		// 409 Conflict means the package already exists — that's fine.
		if statusCode == http.StatusConflict {
			return nil
		}
		return err
	}
	_ = data
	return nil
}

// SetVersionStatus changes the lifecycle status of a package version.
// Valid statuses: active, deprecated, yanked, unpublished, deleted.
func (c *Client) SetVersionStatus(name, version, status string) error {
	payload := map[string]string{"status": status}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, _, err = c.do(http.MethodPatch,
		"/v1/packages/"+packagePath(name)+"/versions/"+encodeQuery(version)+"/status",
		bytes.NewReader(body))
	return err
}
