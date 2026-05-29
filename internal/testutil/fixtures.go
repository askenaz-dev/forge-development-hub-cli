package testutil

import (
	"path/filepath"
	"runtime"
)

// HubFixturePath returns the absolute path to the hub wire-protocol test
// fixture under internal/testutil/fixtures/hub/. The fixture mirrors the
// layout of forge-development-hub with 4 components (1 of each kind) and
// is consumed by the portal-api wire-handler tests.
//
// Resolution uses runtime.Caller so callers do not need to know the
// working directory of `go test`. The returned path is filesystem-clean
// and absolute.
func HubFixturePath() string {
	_, here, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(here), "fixtures", "hub"))
}
