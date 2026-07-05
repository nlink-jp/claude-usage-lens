//go:build linux

package platform

import (
	"os"
	"path/filepath"
)

// NOTE: whether the Claude desktop app / Cowork exists on Linux is unconfirmed;
// the Cowork root defaults under XDG data. The Claude Code root (~/.claude) is
// high-confidence. Override via config [sources] / --source-root as needed.

func xdgDataHome() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

func sourceRoots() (Roots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Roots{}, err
	}
	data, err := xdgDataHome()
	if err != nil {
		return Roots{}, err
	}
	return Roots{
		CodeRoot:   filepath.Join(home, ".claude", "projects"),
		CoworkRoot: filepath.Join(data, "Claude", "local-agent-mode-sessions"),
	}, nil
}

func configDir() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "claude-usage-lens"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "claude-usage-lens"), nil
}

func dataDir() (string, error) {
	data, err := xdgDataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "claude-usage-lens"), nil
}
