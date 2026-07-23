package semver

import (
	"testing"
)

func TestResolveLatest(t *testing.T) {
	avail := []string{"1.0.0", "1.2.0", "2.0.0", "1.1.0"}
	tags := map[string]string{"latest": "2.0.0"}
	got, err := Resolve("latest", avail, tags)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0" {
		t.Errorf("latest = %s, want 2.0.0", got)
	}
}

func TestResolveLatestNoTags(t *testing.T) {
	avail := []string{"1.0.0", "1.2.0", "2.0.0"}
	got, err := Resolve("latest", avail, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0" {
		t.Errorf("latest = %s, want 2.0.0", got)
	}
}

func TestResolveEmptySpec(t *testing.T) {
	avail := []string{"1.0.0", "2.0.0"}
	got, err := Resolve("", avail, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0" {
		t.Errorf("empty spec = %s, want 2.0.0", got)
	}
}

func TestResolveExact(t *testing.T) {
	avail := []string{"1.0.0", "1.2.3", "2.0.0"}
	got, err := Resolve("1.2.3", avail, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.3" {
		t.Errorf("exact = %s, want 1.2.3", got)
	}
}

func TestResolveCaret(t *testing.T) {
	avail := []string{"1.0.0", "1.2.0", "1.5.0", "2.0.0"}
	got, err := Resolve("^1.2.0", avail, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.5.0" {
		t.Errorf("caret = %s, want 1.5.0", got)
	}
}

func TestResolveCaretStaysInMajor(t *testing.T) {
	avail := []string{"1.2.0", "2.0.0", "2.1.0"}
	got, err := Resolve("^1.2.0", avail, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.0" {
		t.Errorf("caret = %s, want 1.2.0", got)
	}
}

func TestResolveTilde(t *testing.T) {
	avail := []string{"1.2.0", "1.2.5", "1.3.0"}
	got, err := Resolve("~1.2.0", avail, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.5" {
		t.Errorf("tilde = %s, want 1.2.5", got)
	}
}

func TestResolveXRange(t *testing.T) {
	avail := []string{"1.0.0", "1.2.0", "1.5.0", "2.0.0"}
	got, err := Resolve("1.x", avail, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.5.0" {
		t.Errorf("x-range = %s, want 1.5.0", got)
	}
}

func TestResolveComparatorSet(t *testing.T) {
	avail := []string{"1.0.0", "1.5.0", "2.0.0"}
	got, err := Resolve(">=1.0.0 <2.0.0", avail, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.5.0" {
		t.Errorf("comparator = %s, want 1.5.0", got)
	}
}

func TestResolveNoMatch(t *testing.T) {
	avail := []string{"1.0.0", "1.2.0"}
	_, err := Resolve("^3.0.0", avail, nil)
	if err == nil {
		t.Fatal("expected error for no match")
	}
}

func TestResolveInvalidSpec(t *testing.T) {
	avail := []string{"1.0.0"}
	_, err := Resolve("not-a-version!!!", avail, nil)
	if err == nil {
		t.Fatal("expected error for invalid spec")
	}
}

func TestResolveEmptyAvailable(t *testing.T) {
	_, err := Resolve("latest", []string{}, nil)
	if err == nil {
		t.Fatal("expected error for empty available")
	}
}

func TestResolveDistTag(t *testing.T) {
	avail := []string{"1.0.0", "2.0.0", "3.0.0-beta"}
	tags := map[string]string{"latest": "2.0.0", "beta": "3.0.0-beta"}
	got, err := Resolve("beta", avail, tags)
	if err != nil {
		t.Fatal(err)
	}
	if got != "3.0.0-beta" {
		t.Errorf("beta tag = %s, want 3.0.0-beta", got)
	}
}

func TestResolveDistTagPointsToMissing(t *testing.T) {
	avail := []string{"1.0.0"}
	tags := map[string]string{"latest": "9.9.9"}
	_, err := Resolve("latest", avail, tags)
	if err == nil {
		t.Fatal("expected error when dist-tag points to missing version")
	}
}

func TestHighestSkipsInvalid(t *testing.T) {
	avail := []string{"notsemver", "1.0.0", "2.0.0", "also-bad"}
	got, err := Highest(avail)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0" {
		t.Errorf("highest = %s, want 2.0.0", got)
	}
}
