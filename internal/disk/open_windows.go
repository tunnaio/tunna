//go:build windows

package disk

import (
	"os"
	"syscall"
)

// openShared opens for reading with FILE_SHARE_DELETE, so an overwrite or
// delete can remove the file while a reader holds it (ADR-0007). os.Open
// omits that flag and would make os.Remove fail with "file in use".
func openShared(p string) (*os.File, error) {
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(name,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: p, Err: err}
	}
	return os.NewFile(uintptr(h), p), nil
}
