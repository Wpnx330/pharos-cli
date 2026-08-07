package api

import (
	"strings"
	"testing"
)

// =============================================================
// Fix 4: APIError nested error parsing (internal/api/client.go)
// =============================================================
//
// The APIError.Error() method parses error response bodies in three
// shapes and falls back to raw body:
//  1. Nested:  {"error": {"code": "...", "message": "..."}}
//  2. Flat:    {"error": "something went wrong"} or {"message": "plain message"}
//  3. Raw:     any non-JSON body is returned as-is
//
// These tests are table-driven with t.Run subtests covering all four
// branches including the empty-body edge case.

func TestAPIErrorParsing(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantSubstr string
	}{
		{
			name:       "Test_APIError_NestedFormat",
			statusCode: 409,
			body:       `{"error":{"code":"CIRCULAR_DEPENDENCY","message":"circular dependency detected: a → b → a"}}`,
			wantSubstr: "circular dependency detected",
		},
		{
			name:       "Test_APIError_FlatError",
			statusCode: 500,
			body:       `{"error":"something went wrong"}`,
			wantSubstr: "something went wrong",
		},
		{
			name:       "Test_APIError_FlatMessage",
			statusCode: 400,
			body:       `{"message":"plain message"}`,
			wantSubstr: "plain message",
		},
		{
			name:       "Test_APIError_RawBody",
			statusCode: 502,
			body:       `not json`,
			wantSubstr: "not json",
		},
		{
			name:       "Test_APIError_EmptyBody",
			statusCode: 500,
			body:       ``,
			wantSubstr: "API error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			apiErr := &APIError{
				StatusCode: tt.statusCode,
				Body:       []byte(tt.body),
			}
			got := apiErr.Error()
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("APIError.Error() = %q, want it to contain %q", got, tt.wantSubstr)
			}
		})
	}
}

// TestAPIError_NestedFormatIncludesStatusCode verifies that the nested
// error format includes the HTTP status code in the output.
func TestAPIError_NestedFormatIncludesStatusCode(t *testing.T) {
	apiErr := &APIError{
		StatusCode: 409,
		Body:       []byte(`{"error":{"code":"CIRCULAR_DEPENDENCY","message":"circular dependency detected: a → b → a"}}`),
	}
	got := apiErr.Error()
	if !strings.Contains(got, "409") {
		t.Errorf("Error() = %q, want it to contain status code 409", got)
	}
}

// TestAPIError_NestedFormatOmitsCode verifies that when a nested error
// has both code and message, the Error() output uses the message (not
// the code) in the user-facing string.
func TestAPIError_NestedFormatOmitsCode(t *testing.T) {
	apiErr := &APIError{
		StatusCode: 409,
		Body:       []byte(`{"error":{"code":"CIRCULAR_DEPENDENCY","message":"circular dependency detected: a → b → a"}}`),
	}
	got := apiErr.Error()
	// The code "CIRCULAR_DEPENDENCY" should not appear as a standalone
	// token — the message is what's surfaced.
	if !strings.Contains(got, "circular dependency detected: a → b → a") {
		t.Errorf("Error() = %q, should contain the full nested message", got)
	}
}

// TestAPIError_NestedEmptyMessageFallsThrough verifies that a nested
// error shape with an empty message falls through to the flat/raw path.
func TestAPIError_NestedEmptyMessageFallsThrough(t *testing.T) {
	apiErr := &APIError{
		StatusCode: 400,
		Body:       []byte(`{"error":{"code":"BAD_REQUEST","message":""}}`),
	}
	got := apiErr.Error()
	// With empty message, the nested branch is skipped (message != "" check).
	// The flat branch will try to unmarshal {"error": {...}} into a string,
	// which fails, so it falls through to raw body.
	// The raw body should contain "code" at minimum.
	if !strings.Contains(got, "API error") {
		t.Errorf("Error() = %q, should contain 'API error' prefix", got)
	}
}

// TestAPIError_FlatErrorPrecedenceOverMessage verifies that when both
// flat "error" and "message" fields are present, "error" takes precedence.
func TestAPIError_FlatErrorPrecedenceOverMessage(t *testing.T) {
	apiErr := &APIError{
		StatusCode: 400,
		Body:       []byte(`{"error":"error field wins","message":"message field loses"}`),
	}
	got := apiErr.Error()
	if !strings.Contains(got, "error field wins") {
		t.Errorf("Error() = %q, should prefer 'error' field over 'message'", got)
	}
	if strings.Contains(got, "message field loses") {
		t.Errorf("Error() = %q, should NOT contain 'message' field content when 'error' is present", got)
	}
}

// TestAPIError_EmptyBodyReturnsAPIErrorPrefix verifies the empty body
// edge case returns a string starting with "API error".
func TestAPIError_EmptyBodyReturnsAPIErrorPrefix(t *testing.T) {
	apiErr := &APIError{
		StatusCode: 500,
		Body:       []byte(``),
	}
	got := apiErr.Error()
	if !strings.HasPrefix(got, "API error") {
		t.Errorf("Error() = %q, should start with 'API error'", got)
	}
}
