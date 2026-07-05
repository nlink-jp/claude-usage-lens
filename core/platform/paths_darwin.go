//go:build darwin

package platform

import (
	"os"
	"path/filepath"
)

func sourceRoots() (Roots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Roots{}, err
	}
	claudeAppSupport := filepath.Join(home, "Library", "Application Support", "Claude")
	return Roots{
		CodeRoot:   filepath.Join(home, ".claude", "projects"),
		CoworkRoot: filepath.Join(claudeAppSupport, "local-agent-mode-sessions"),
	}, nil
}

func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "claude-usage-lens"), nil
}

func dataDir() (string, error) { return configDir() }
