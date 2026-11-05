//go:build !windows

// input: Linux/macOS 的已打开锁文件。
// output: 非阻塞独占文件锁，关闭文件时由内核释放。
// pos: Store.Lock 的 Unix 平台实现。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package store

import (
	"errors"
	"os"
	"syscall"
)

func lockFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return ErrBusy
	}
	return err
}
