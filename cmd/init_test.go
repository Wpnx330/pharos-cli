package cmd

import (
	"testing"
)

func TestParseDependencyInput(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantName    string
		wantVersion string
		wantErr     bool
	}{
		// Format 1: bare name → any version
		{"bare name", "lodash", "lodash", "*", false},
		{"bare scoped name", "@scope/pkg", "@scope/pkg", "*", false},

		// Format 2: name@latest → "latest"
		{"at latest", "lodash@latest", "lodash", "latest", false},

		// Format 3: name>=0.1.0 → ">=0.1.0"
		{"semver min", "lodash>=0.1.0", "lodash", ">=0.1.0", false},

		// Format 4: name=0.1.0 → "=0.1.0"
		{"exact version", "lodash=0.1.0", "lodash", "=0.1.0", false},

		// Format 5: name<=0.1.0 → "<=0.1.0"
		{"semver max", "lodash<=0.1.0", "lodash", "<=0.1.0", false},

		// Format 6: name^1.0.0 → "^1.0.0"
		{"compatible version", "lodash^1.0.0", "lodash", "^1.0.0", false},

		// Edge cases: whitespace
		{"leading/trailing whitespace", "  lodash  ", "lodash", "*", false},
		{"whitespace around constraint", "lodash >= 0.1.0", "lodash", ">= 0.1.0", false},

		// Edge cases: empty / no name
		{"empty string", "", "", "", true},
		{"only whitespace", "   ", "", "", true},
		{"only operator", ">=0.1.0", "", "", true},       // idx == 0, no name
		{"trailing @ with no version", "lodash@", "", "", true},
		{"trailing = with no version", "lodash=", "", "", true},

		// Edge cases: invalid characters
		{"invalid char space in name", "lod ash", "", "", true},
		{"invalid char exclamation", "lodash!1.0.0", "", "", true},

		// Name with dots, hyphens, underscores (valid)
		{"dotted name", "lodash.foo", "lodash.foo", "*", false},
		{"hyphenated name", "my-pkg", "my-pkg", "*", false},
		{"underscore name", "my_pkg", "my_pkg", "*", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotVersion, err := parseDependencyInput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseDependencyInput(%q) expected error, got nil (name=%q, version=%q)",
						tt.input, gotName, gotVersion)
				}
				return
			}
			if err != nil {
				t.Errorf("parseDependencyInput(%q) unexpected error: %v", tt.input, err)
				return
			}
			if gotName != tt.wantName {
				t.Errorf("parseDependencyInput(%q) name = %q, want %q", tt.input, gotName, tt.wantName)
			}
			if gotVersion != tt.wantVersion {
				t.Errorf("parseDependencyInput(%q) version = %q, want %q", tt.input, gotVersion, tt.wantVersion)
			}
		})
	}
}
