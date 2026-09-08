package procs

import "testing"

// TestParseTasklistMemUsage pins the tasklist CSV parsing used by the
// Windows RSS probe. The parse helper lives in a build-tag-free file, so
// this table runs on every platform even though the probe is windows-only.
func TestParseTasklistMemUsage(t *testing.T) {
	const kb = int64(1024)
	tests := []struct {
		name   string
		raw    string
		want   int64
		wantOK bool
	}{
		{
			name:   "quoted mem usage with thousands separator",
			raw:    `"chrome.exe","1234","Console","1","7,524 K"`,
			want:   7524 * kb,
			wantOK: true,
		},
		{
			name:   "plain kilobyte figure",
			raw:    `"app.exe","99","Service","0","4096 K"`,
			want:   4096 * kb,
			wantOK: true,
		},
		{
			name:   "CRLF-terminated row",
			raw:    "\"app.exe\",\"99\",\"Console\",\"1\",\"7,524 K\"\r\n",
			want:   7524 * kb,
			wantOK: true,
		},
		{
			name:   "no matching task (INFO line)",
			raw:    "INFO: No tasks are running which match the specified criteria.",
			wantOK: false,
		},
		{
			name:   "empty output",
			raw:    "",
			wantOK: false,
		},
		{
			name:   "whitespace-only output",
			raw:    "   \r\n",
			wantOK: false,
		},
		{
			name:   "row missing the mem-usage column",
			raw:    `"a.exe","123","Console","1"`,
			wantOK: false,
		},
		{
			name:   "bare single column",
			raw:    `"a.exe"`,
			wantOK: false,
		},
		{
			name:   "non-numeric German-locale separator",
			raw:    `"app.exe","1","Console","1","75.524 K"`,
			wantOK: false,
		},
		{
			name:   "negative figure",
			raw:    `"app.exe","1","Console","1","-1 K"`,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseTasklistMemUsage(tt.raw)
			if ok != tt.wantOK {
				t.Fatalf("parseTasklistMemUsage(%q) ok = %v, want %v", tt.raw, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("parseTasklistMemUsage(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}
