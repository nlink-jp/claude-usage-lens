package platform

import (
	"strings"
	"testing"
)

// These tests run on whatever OS `go test` is invoked on and assert the shared
// contract every platform file must satisfy: non-empty, plausible paths.
func TestSourceRoots(t *testing.T) {
	r, err := SourceRoots()
	if err != nil {
		t.Fatalf("SourceRoots() error: %v", err)
	}
	if r.CodeRoot == "" || r.CoworkRoot == "" {
		t.Fatalf("empty roots: %+v", r)
	}
	if !strings.Contains(r.CodeRoot, ".claude") {
		t.Errorf("CodeRoot should live under .claude, got %q", r.CodeRoot)
	}
}

func TestAppDirs(t *testing.T) {
	c, err := ConfigDir()
	if err != nil || c == "" {
		t.Fatalf("ConfigDir() = %q, err %v", c, err)
	}
	d, err := DataDir()
	if err != nil || d == "" {
		t.Fatalf("DataDir() = %q, err %v", d, err)
	}
	if !strings.Contains(c, "claude-usage-lens") || !strings.Contains(d, "claude-usage-lens") {
		t.Errorf("app dirs should be namespaced: config=%q data=%q", c, d)
	}
}
