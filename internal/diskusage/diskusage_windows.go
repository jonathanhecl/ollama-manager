//go:build windows

package diskusage

import (
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	procGetDiskFreeSpaceExW    = kernel32.NewProc("GetDiskFreeSpaceExW")
)

// ForPath returns total and free bytes for the disk that contains path.
func ForPath(path string) (totalBytes uint64, freeBytes uint64, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, 0, err
	}
	p, err := syscall.UTF16PtrFromString(abs)
	if err != nil {
		return 0, 0, err
	}

	var freeAvailable uint64
	var total uint64
	var freeTotal uint64

	r1, _, callErr := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&freeAvailable)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&freeTotal)),
	)
	if r1 == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return 0, 0, callErr
		}
		return 0, 0, syscall.EINVAL
	}
	return total, freeTotal, nil
}
