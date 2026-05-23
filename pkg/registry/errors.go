package registry

import "fmt"

// RegistryUnreachable is the error returned when the registry data cannot be
// loaded at all. The CLI maps this to exit code 3.
type RegistryUnreachable struct {
	Detail string
}

func (e RegistryUnreachable) Error() string {
	return fmt.Sprintf("registry unreachable: %s", e.Detail)
}

// HashMismatch is returned when a fetched bundle's canonical hash does not
// match the recorded bundle.sha256. The CLI maps this to exit code 1.
type HashMismatch struct {
	Expected string
	Got      string
}

func (e HashMismatch) Error() string {
	return fmt.Sprintf("hash mismatch: expected %s got %s", e.Expected, e.Got)
}
