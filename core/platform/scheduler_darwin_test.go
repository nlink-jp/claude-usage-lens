//go:build darwin

package platform

import "testing"

func TestRenderDaemonConfig(t *testing.T) {
	out, err := renderDaemonConfig("/usr/local/bin/claude-usage-lens", 900)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"com.nlink-jp.claude-usage-lens",
		"<string>/usr/local/bin/claude-usage-lens</string>",
		"<string>ingest</string>",
		"<integer>900</integer>",
		"<key>RunAtLoad</key>",
	} {
		if !contains(out, want) {
			t.Errorf("plist missing %q\n%s", want, out)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
