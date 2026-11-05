// input: Windows 的已打开锁文件。
// output: 非阻塞独占字节范围锁，关闭句柄时由系统释放。
// pos: Store.Lock 的 Windows 平台实现。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

func lockFile(file *os.File) error {
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrBusy
	}
	return err
}
