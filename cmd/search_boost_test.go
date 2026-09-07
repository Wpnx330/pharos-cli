package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Wpnx330/pharos-cli/internal/api"
	"github.com/Wpnx330/pharos-cli/internal/ui"
)

// captureSearchStdout captures fmt.Printf output written directly to
// os.Stdout while fn runs (the cmd-package capture pattern).
func captureSearchStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	ch := make(chan string, 1)
	go func() {
		var b strings.Builder
		_, _ = io.Copy(&b, r)
		ch <- b.String()
	}()
	fn()
	os.Stdout = orig
	_ = w.Close()
	out := <-ch
	_ = r.Close()
	return out
}

func boostedHit(name string) api.SearchResult {
	return api.SearchResult{
		Name:           name,
		Version:        "1.0.0",
		Description:    "boosted package",
		Transport:      []string{"stdio"},
		SourceRegistry: "pharos",
		Sponsored:      true,
	}
}

func organicHits() []api.SearchResult {
	return []api.SearchResult{
		{Name: "alpha/one", Version: "1.0.0", Description: "first"},
		{Name: "beta/two", Version: "2.0.0", Description: "second"},
	}
}

func TestSponsoredSectionRenderedAboveOrganic(t *testing.T) {
	resp := &api.SearchResponse{
		Results: organicHits(),
		Boosted: []api.SearchResult{boostedHit("gamma/three")},
		Total:   2,
	}
	out := captureSearchStdout(t, func() {
		renderSearchResults(resp, "query", 1, "", "")
	})

	sponsoredIdx := strings.Index(out, sponsoredHeader)
	organicIdx := strings.Index(out, "alpha/one")
	if sponsoredIdx < 0 {
		t.Fatalf("output missing SPONSORED header:\n%s", out)
	}
	if organicIdx < 0 {
		t.Fatalf("output missing organic rows:\n%s", out)
	}
	if sponsoredIdx > organicIdx {
		t.Errorf("SPONSORED section must render above organic results:\n%s", out)
	}
	if !strings.Contains(out, "gamma/three [boosted]") {
		t.Errorf("boosted row missing name + [boosted] marker:\n%s", out)
	}
}

func TestNoSponsoredSectionWhenEmpty(t *testing.T) {
	resp := &api.SearchResponse{Results: organicHits(), Total: 2}
	out := captureSearchStdout(t, func() {
		renderSearchResults(resp, "query", 1, "", "")
	})
	if strings.Contains(out, sponsoredHeader) {
		t.Errorf("empty boosted must produce zero extra output:\n%s", out)
	}
	if strings.Contains(out, sponsoredMarker) {
		t.Errorf("empty boosted must not render the marker:\n%s", out)
	}

	// Organic table bytes unchanged: the output is exactly the organic
	// table + footer + count, assembled from the same primitives the
	// pre-boosts code path used.
	cols := searchTableColumns()
	var rows []ui.TableRow
	for _, r := range resp.Results {
		rows = append(rows, searchTableRow(r))
	}
	want := ui.RenderTable(cols, rows) +
		searchInfoFooter() + "\n" +
		"\n" + ui.Muted.Render("2 package(s) found") + "\n"
	if out != want {
		t.Errorf("organic rendering changed:\n got %q\nwant %q", out, want)
	}
}

func TestSponsoredCapTwoDisplayed(t *testing.T) {
	resp := &api.SearchResponse{
		Results: organicHits(),
		Boosted: []api.SearchResult{
			boostedHit("one/pkg"),
			boostedHit("two/pkg"),
			boostedHit("three/pkg"),
		},
		Total: 2,
	}
	out := captureSearchStdout(t, func() {
		renderSearchResults(resp, "query", 1, "", "")
	})
	if got := strings.Count(out, sponsoredMarker); got != 2 {
		t.Errorf("rendered sponsored rows = %d, want 2 (max slots):\n%s", got, out)
	}
}

func TestSponsoredRowsKeepOrganicRowShape(t *testing.T) {
	// Same columns and cell renderers as organic; only the NAME cell
	// carries the marker. SECURITY stays dash for an unscored boost.
	row := sponsoredTableRows([]api.SearchResult{boostedHit("x/pkg")})[0]
	org := searchTableRow(boostedHit("x/pkg"))
	if len(row) != len(org) {
		t.Fatalf("sponsored row width = %d, want %d (same as organic)", len(row), len(org))
	}
	for i := 1; i < len(row); i++ {
		if row[i] != org[i] {
			t.Errorf("sponsored cell %d = %q, want organic %q", i, row[i], org[i])
		}
	}
	if !strings.Contains(row[0], "[boosted]") {
		t.Errorf("NAME cell missing marker: %q", row[0])
	}
}

func TestSearchResponseJSONRoundTripBoosted(t *testing.T) {
	// The server contract: boosted always present (empty array when
	// none), sponsored only on paid entries. --json must echo both.
	server := map[string]any{
		"results": []map[string]any{
			{"name": "organic/pkg", "version": "1.0.0", "description": "d", "score": 1.5,
				"downloads30d": 10, "publisher": map[string]any{"namespace": "ns"}},
		},
		"boosted": []map[string]any{
			{"name": "io.github.org/remote", "sponsored": true},
		},
		"nextCursor": "",
		"total":      1,
	}
	raw, err := json.Marshal(server)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var resp api.SearchResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Boosted) != 1 {
		t.Fatalf("Boosted = %d, want 1", len(resp.Boosted))
	}
	if !resp.Boosted[0].Sponsored {
		t.Error("Boosted[0].Sponsored = false, want true")
	}
	if resp.Boosted[0].Name != "io.github.org/remote" {
		t.Errorf("Boosted[0].Name = %q", resp.Boosted[0].Name)
	}
	if resp.Results[0].Sponsored {
		t.Error("organic hit parsed Sponsored = true")
	}

	// --json echoes the contract: sponsored omitted on organic rows,
	// boosted key always present.
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var echo map[string]json.RawMessage
	if err := json.Unmarshal(data, &echo); err != nil {
		t.Fatalf("echo unmarshal: %v", err)
	}
	if _, ok := echo["boosted"]; !ok {
		t.Error("--json output must always carry the boosted key")
	}
	org := string(echo["results"])
	if strings.Contains(org, "sponsored") {
		t.Errorf("--json organic rows must omit sponsored (server contract): %s", org)
	}
	boosted := string(echo["boosted"])
	if !strings.Contains(boosted, `"sponsored":true`) {
		t.Errorf("--json boosted rows must carry sponsored:true: %s", boosted)
	}
}

func TestSearchJSONEmptyBoostedEchoesEmptyArray(t *testing.T) {
	// The client normalizes the boosted key so --json always emits an
	// explicit empty array (current server contract), whether the
	// registry sent [] or omitted the key entirely (pre-W4.2).
	for _, tc := range []struct {
		name   string
		server string
	}{
		{"explicit empty array", `{"results":[],"boosted":[],"nextCursor":"","total":0}`},
		{"key absent (older registry)", `{"results":[],"nextCursor":"","total":0}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.server))
			}))
			t.Cleanup(srv.Close)
			resp, err := api.New(srv.URL, "").Search(api.SearchParams{Query: "q"})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			data, err := json.Marshal(resp)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var raw map[string]any
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			boosted, ok := raw["boosted"].([]any)
			if !ok {
				t.Fatalf("boosted key missing or not an array: %v (%T)", raw["boosted"], raw["boosted"])
			}
			if len(boosted) != 0 {
				t.Errorf("boosted = %v, want empty array", boosted)
			}
		})
	}
}
