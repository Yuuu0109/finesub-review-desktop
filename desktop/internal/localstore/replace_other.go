//go:build !windows

package localstore

import "os"

// ReplaceFile atomically replaces a file. The source must be in the same
// filesystem as the destination and closed before this call.
func ReplaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
