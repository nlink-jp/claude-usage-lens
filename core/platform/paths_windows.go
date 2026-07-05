//go:build windows

package platform

import (
	"os"
	"path/filepath"
)

// NOTE: these Windows paths are INFERRED and unverified on real hardware (RFP §7).
// The Claude Code root (%USERPROFILE%\.claude) is high-confidence; the Cowork root
// under %APPDATA%\Claude is a guess (could be Anthropic\Claude, or absent entirely).
// Users override via config [sources] / --source-root; `doctor` reports what resolved.

func roamingAppData() (string, error) {
	if v := os.Getenv("APPDATA"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "AppData", "Roaming"), nil
}

func localAppData() (string, error) {
	if v := os.Getenv("LOCALAPPDATA"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "AppData", "Local"), nil
}

func sourceRoots() (Roots, error) {
	home, err := os.UserHomeDir() // %USERPROFILE%
	if err != nil {
		return Roots{}, err
	}
	appData, err := roamingAppData()
	if err != nil {
		return Roots{}, err
	}
	return Roots{
		CodeRoot:   filepath.Join(home, ".claude", "projects"),
		CoworkRoot: filepath.Join(appData, "Claude", "local-agent-mode-sessions"),
	}, nil
}

func configDir() (string, error) {
	appData, err := roamingAppData()
	if err != nil {
		return "", err
	}
	return filepath.Join(appData, "claude-usage-lens"), nil
}

func dataDir() (string, error) {
	local, err := localAppData()
	if err != nil {
		return "", err
	}
	return filepath.Join(local, "claude-usage-lens"), nil
}
