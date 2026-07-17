//go:build !unix

package artifact

import "os"

func sysNlink(info os.FileInfo) uint64 { return 1 }
