package portalapi

import "os"

// readFile is wrapped so it can be stubbed in tests if needed.
var readFile = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}
