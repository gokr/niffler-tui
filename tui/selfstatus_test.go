package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// selfModel builds the minimum model /status needs to render the tui line.
func selfModel(loc Locale, self selfIdentity) model {
	return model{loc: loc, selfIdentity: self}
}

// TestSelfStatusLineFull covers a managed install: version, resolved binary
// path, and the plugin package's pinned ref + commit.
func TestSelfStatusLineFull(t *testing.T) {
	m := selfModel("en", selfIdentity{
		Version: "0.1.0",
		Binary:  "/home/gokr/git/nifflerprod/var/bin/tui",
		Package: "niffler-tui",
		Ref:     "main",
		Commit:  "204410344739842fd85ec3f854c324b1afa08ca5",
	})
	got := m.selfStatusLine()
	if !strings.HasPrefix(got, "tui: 0.1.0 ") {
		t.Errorf("expected version prefix, got %q", got)
	}
	if !strings.Contains(got, "/home/gokr/git/nifflerprod/var/bin/tui") {
		t.Errorf("expected binary path, got %q", got)
	}
	if !strings.Contains(got, "niffler-tui @ main 2044103") {
		t.Errorf("expected package, ref and short commit, got %q", got)
	}
}

// TestSelfStatusLineDegrades pins the guarantee that the line still renders
// when the plugin lookup found nothing (no plugins component, a `make build`
// binary, or a request error).
func TestSelfStatusLineDegrades(t *testing.T) {
	bare := selfModel("en", selfIdentity{Version: "0.1.0"})
	if got := bare.selfStatusLine(); !strings.Contains(got, "0.1.0") {
		t.Errorf("expected version without a binary, got %q", got)
	}

	unmanaged := selfModel("en", selfIdentity{Version: "0.1.0", Binary: "/tmp/tui"})
	got := unmanaged.selfStatusLine()
	if !strings.Contains(got, "/tmp/tui") || !strings.Contains(got, "0.1.0") {
		t.Errorf("expected version and path without provenance, got %q", got)
	}
	if strings.Contains(got, "@") {
		t.Errorf("unmanaged binary must not claim a ref, got %q", got)
	}

	// Version present, provenance partial: the ref must still not invent a
	// commit, and an empty version must fall back rather than render "tui: ".
	partial := selfModel("en", selfIdentity{Binary: "/tmp/tui", Package: "niffler-tui"})
	got = partial.selfStatusLine()
	if !strings.Contains(got, "unknown") {
		t.Errorf("expected unknown-version fallback, got %q", got)
	}
}

// TestSelfStatusLineShortCommit guards the slice boundary: a commit shorter
// than 7 characters must not panic or be truncated to nothing.
func TestSelfStatusLineShortCommit(t *testing.T) {
	for _, commit := range []string{"", "abc", "1234567", "12345678"} {
		m := selfModel("en", selfIdentity{
			Version: "0.1.0", Binary: "/tmp/tui", Package: "p", Ref: "main", Commit: commit,
		})
		got := m.selfStatusLine()
		if !strings.Contains(got, "/tmp/tui") {
			t.Errorf("commit %q: lost the binary path: %q", commit, got)
		}
		if commit != "" && !strings.Contains(got, commit[:shortCommitLen(len(commit))]) {
			t.Errorf("commit %q: expected short form in %q", commit, got)
		}
	}
}

// shortCommitLen is how much of a commit hash /status shows, clamped so a
// short hash is not sliced out of range.
func shortCommitLen(n int) int {
	if n > 7 {
		return 7
	}
	return n
}

// TestInstalledPackageDecodesRealReply decodes the exact shape plugins
// returns today. The live record has NO "commit" field (the install path
// only records ref), so provenance must survive on ref alone.
func TestInstalledPackageDecodesRealReply(t *testing.T) {
	raw := `{"packages":[{"name":"niffler-tui","repo":"gokr/niffler-tui",
	  "ref":"main","dir":"/home/gokr/git/nifflerprod/var/plugins/niffler-tui@main",
	  "version":"0.1.0","components":[{"name":"tui",
	  "binary":"/home/gokr/git/nifflerprod/var/bin/tui","interactive":true,
	  "spawned":false,"built":"source"}],"addedAt":1788896151.3357365}]}`
	var listed struct {
		Packages []installedPackage `json:"packages"`
	}
	if err := json.Unmarshal([]byte(raw), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(listed.Packages))
	}
	pkg := listed.Packages[0]
	if pkg.Name != "niffler-tui" || pkg.Ref != "main" || pkg.Commit != "" {
		t.Errorf("unexpected package: %+v", pkg)
	}
	if len(pkg.Components) != 1 || pkg.Components[0].Binary != "/home/gokr/git/nifflerprod/var/bin/tui" {
		t.Fatalf("unexpected components: %+v", pkg.Components)
	}

	// The matched package must render with its ref even without a commit.
	m := selfModel("en", selfIdentity{
		Version: "0.1.0", Binary: pkg.Components[0].Binary,
		Package: pkg.Name, Ref: pkg.Ref, Commit: pkg.Commit,
	})
	got := m.selfStatusLine()
	if !strings.Contains(got, "niffler-tui @ main") {
		t.Errorf("expected ref-only provenance, got %q", got)
	}
	if strings.HasSuffix(got, "main ") {
		t.Errorf("expected no trailing commit gap, got %q", got)
	}
}

// TestSelfStatusLineLocalized keeps the new key honest across locales.
func TestSelfStatusLineLocalized(t *testing.T) {
	for _, loc := range []Locale{"en", "zh", "zh-TW"} {
		if _, ok := catalogs[loc]["status.detailTui"]; !ok {
			t.Errorf("locale %q is missing status.detailTui", loc)
		}
	}
}
