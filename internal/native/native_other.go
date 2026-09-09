//go:build !darwin || !cgo

package native

import "fmt"

// Available reports whether this build includes the native reader.
func Available() bool { return false }

// Run reports that this build lacks the native reader.
func Run(State) (Result, error) {
	return Result{}, fmt.Errorf("native reader requires macOS with cgo")
}
