package api

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	UploadID  string `json:"uploadId"`
	URL       string `json:"url"`
	BlobKey   string `json:"blobKey"`
	Presigned string `json:"presignedUrl"`
}

// Upload initiates an upload by posting the tarball metadata to the
// registry. The returned UploadResponse contains the upload session ID.
func (c *Client) Upload(tarballName string, tarballSize int64) (*UploadResponse, error) {
	payload := map[string]any{
		"filename": tarballName,
		"size":     tarballSize,
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

// PublishRequest is the body sent to the PUT /v1/packages/<name> endpoint.
type PublishRequest struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	License     string            `json:"license"`
	Homepage    string            `json:"homepage"`
	Repository  string            `json:"repository"`
	Bin         string            `json:"bin"`
	Files       []string          `json:"files"`
	UploadID    string            `json:"uploadId,omitempty"`
	DistTags    map[string]string `json:"distTags,omitempty"`
}

// Publish registers a new package version with the registry.
func (c *Client) Publish(name string, req *PublishRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, _, err = c.do(http.MethodPut, fmt.Sprintf("/v1/packages/%s", encodeQuery(name)), bytes.NewReader(body))
	return err
}
