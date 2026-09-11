//go:build !windows

package disk

import "os"

func openShared(p string) (*os.File, error) {
	return os.Open(p)
}
