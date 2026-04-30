//go:build !windows

package diskusage

import "syscall"

// ForPath returns total and free bytes for the filesystem that contains path.
func ForPath(path string) (totalBytes uint64, freeBytes uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}

	blkSize := uint64(st.Bsize)
	total := st.Blocks * blkSize
	free := st.Bavail * blkSize
	return total, free, nil
}
