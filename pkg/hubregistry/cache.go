package hubregistry

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultCacheDir resolves the per-user cache directory the hub clone
// is materialised in:
//
//   - Linux/macOS: $XDG_CACHE_HOME/fdh/hub or ~/.cache/fdh/hub
//   - Windows: %LOCALAPPDATA%\fdh\hub
//
// Returns the empty string when neither $HOME nor %LOCALAPPDATA% can
// be resolved. Callers should treat that as a fatal configuration
// error.
func DefaultCacheDir() string {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "fdh", "hub")
		}
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		return filepath.Join(home, "AppData", "Local", "fdh", "hub")
	}

	if base := os.Getenv("XDG_CACHE_HOME"); base != "" {
		return filepath.Join(base, "fdh", "hub")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".cache", "fdh", "hub")
}
