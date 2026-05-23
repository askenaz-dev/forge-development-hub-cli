//go:build windows

package cli

import (
	"fmt"

	"golang.org/x/sys/windows/registry"
)

// windowsLongPathLimit is the legacy MAX_PATH boundary on Windows. Paths at
// or below this length are always safe; longer paths only succeed when the
// kernel-level long-path support is enabled (HKLM\SYSTEM\CurrentControlSet
// \Control\FileSystem\LongPathsEnabled = 1) AND the calling process opts in
// via its manifest. This binary's manifest declares long-path awareness,
// so the registry check is the sole gate.
const windowsLongPathLimit = 260

// CheckLongPath returns a non-nil error when path is longer than the legacy
// MAX_PATH limit and long-path support is not enabled on the host. The
// error message points the developer at the Windows setting to flip.
func CheckLongPath(path string) error {
	if len(path) <= windowsLongPathLimit {
		return nil
	}
	enabled, detail := longPathsEnabledFromRegistry()
	if enabled {
		return nil
	}
	return fmt.Errorf(
		"destination path is %d characters which exceeds the Windows legacy "+
			"limit of %d, and the LongPathsEnabled registry value is %s. "+
			"Enable long-path support by setting "+
			"HKLM\\SYSTEM\\CurrentControlSet\\Control\\FileSystem\\LongPathsEnabled=1 "+
			"(requires admin and a reboot), or shorten the path. Path: %s",
		len(path), windowsLongPathLimit, detail, ellipsizeMiddle(path, 80),
	)
}

func ellipsizeMiddle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	half := (max - 3) / 2
	return s[:half] + "..." + s[len(s)-half:]
}

// longPathsEnabledFromRegistry reads HKLM\SYSTEM\CurrentControlSet\Control
// \FileSystem\LongPathsEnabled and returns whether long-path support is
// active. The second return value is a short human-readable description
// of the registry state for inclusion in error messages.
func longPathsEnabledFromRegistry() (bool, string) {
	const keyPath = `SYSTEM\CurrentControlSet\Control\FileSystem`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, keyPath, registry.QUERY_VALUE)
	if err != nil {
		return false, fmt.Sprintf("not readable (%v)", err)
	}
	defer k.Close()

	v, _, err := k.GetIntegerValue("LongPathsEnabled")
	if err != nil {
		if err == registry.ErrNotExist {
			return false, "missing (defaults to 0)"
		}
		return false, fmt.Sprintf("unreadable (%v)", err)
	}
	if v == 1 {
		return true, "1"
	}
	return false, fmt.Sprintf("%d", v)
}
