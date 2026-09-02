package api

import (
	"encoding/json"
	"testing"
)

// The detail endpoint serves the same packages table as search, so
// publisher arrives as an object there too; the flatten must be tolerant
// of null (guard) and absent keys.
func TestPackageDetailUnmarshalPublisherAndCategory(t *testing.T) {
	raw := `{
  "name": "context7",
  "description": "docs",
  "publisher": {"namespace": "upstash", "extra": true},
  "category": "documentation"
}`
	var got PackageDetail
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Publisher != "upstash" {
		t.Errorf("Publisher = %q, want flattened namespace upstash", got.Publisher)
	}
	if got.Category != "documentation" {
		t.Errorf("Category = %q, want documentation", got.Category)
	}
}

func TestPackageDetailUnmarshalNullPublisherDoesNotPanic(t *testing.T) {
	raw := `{"name": "anon", "publisher": null, "category": ""}`
	var got PackageDetail
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal with null publisher: %v", err)
	}
	if got.Publisher != "" {
		t.Errorf("Publisher = %q, want empty when null", got.Publisher)
	}
}

func TestPackageDetailUnmarshalWithoutSignalFields(t *testing.T) {
	raw := `{"name": "bare", "versions": []}`
	var got PackageDetail
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Publisher != "" || got.Category != "" {
		t.Errorf("signals = %q/%q, want empty when keys absent", got.Publisher, got.Category)
	}
}
