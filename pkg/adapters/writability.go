package adapters

import (
	"fmt"
	"os"
	"path/filepath"
)

// Writability is the result of CheckWritable for one destination path.
type Writability int

const (
	// WritableExisting means the directory already exists and the current
	// user can create files in it.
	WritableExisting Writability = iota
	// WritableCreatable means the directory does not exist but its
	// closest existing ancestor is writable so the installer can mkdir
	// it on demand.
	WritableCreatable
	// Unwritable means neither the directory nor any creatable ancestor
	// can be written by the current user.
	Unwritable
)

// WritabilityReport records the outcome of CheckWritable for one path.
type WritabilityReport struct {
	Path  string
	State Writability
	// Detail carries the underlying error or condition the report is based
	// on. Empty for the "writable existing" success case.
	Detail string
}

// CheckWritable inspects a destination path. The function does NOT create
// the directory; it only reports whether the installer would succeed
// later. Probing via a temp file is the most reliable check for write
// permission since mode bits on Windows are misleading.
func CheckWritable(path string) WritabilityReport {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return WritabilityReport{
				Path:   path,
				State:  Unwritable,
				Detail: "destination exists but is not a directory",
			}
		}
		if probeDirWritable(path) {
			return WritabilityReport{Path: path, State: WritableExisting}
		}
		return WritabilityReport{
			Path:   path,
			State:  Unwritable,
			Detail: "directory exists but is not writable by current user",
		}
	}
	if !os.IsNotExist(err) {
		return WritabilityReport{Path: path, State: Unwritable, Detail: err.Error()}
	}
	// Path does not exist — walk up to find the closest existing parent.
	parent := path
	for {
		p := filepath.Dir(parent)
		if p == parent {
			// reached filesystem root without finding an existing parent
			return WritabilityReport{
				Path:   path,
				State:  Unwritable,
				Detail: "no existing ancestor directory found",
			}
		}
		parent = p
		info, err := os.Stat(parent)
		if err == nil && info.IsDir() {
			if probeDirWritable(parent) {
				return WritabilityReport{
					Path:   path,
					State:  WritableCreatable,
					Detail: fmt.Sprintf("will be created under %s", parent),
				}
			}
			return WritabilityReport{
				Path:   path,
				State:  Unwritable,
				Detail: fmt.Sprintf("nearest existing ancestor %s is not writable", parent),
			}
		}
	}
}

// probeDirWritable attempts to create a unique temp file in dir. It returns
// true on success and immediately removes the probe file. This is the only
// reliable cross-platform check for write permission.
func probeDirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".fdh-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}
