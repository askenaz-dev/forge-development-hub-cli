//go:build !windows

package cli

// CheckLongPath is a no-op on non-Windows platforms.
func CheckLongPath(path string) error {
	return nil
}
