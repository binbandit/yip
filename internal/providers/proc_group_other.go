//go:build !linux

package providers

import "syscall"

func processGroupExited(pgid int) bool {
	return syscall.Kill(-pgid, 0) == syscall.ESRCH
}
