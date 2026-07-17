//go:build unix

package artifact

import (
	"os"
	"syscall"
)

func sysNlink(info os.FileInfo) uint64 {
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(stat.Nlink)
	}
	return 1
}
