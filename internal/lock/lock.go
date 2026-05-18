// Package lock implements a simple cross-process file lock so two
// lincrawl sync runs cannot race on the same SQLite archive.
package lock

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type FileLock struct {
	path string
	file *os.File
}

// Acquire creates `<dbPath>.lock` exclusively. Fails fast if the lock
// already exists; caller should surface the lockholder pid.
func Acquire(dbPath string) (*FileLock, error) {
	if dbPath == "" {
		return nil, errors.New("lock: empty path")
	}
	path := dbPath + ".lock"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			holder, _ := os.ReadFile(path)
			return nil, fmt.Errorf("store is locked: %s (holder pid=%s)", path, string(holder))
		}
		return nil, err
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid())); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return &FileLock{path: path, file: f}, nil
}

func (l *FileLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	closeErr := l.file.Close()
	rmErr := os.Remove(l.path)
	l.file = nil
	if closeErr != nil {
		return closeErr
	}
	return rmErr
}
