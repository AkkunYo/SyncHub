//go:build darwin || linux

package config

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockConfigFile(file *os.File) error {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return ErrLocked
	}
	return err
}

func unlockConfigFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func syncConfigDirectory(directory string) error {
	directoryFile, err := os.Open(directory)
	if err != nil {
		return nil
	}
	defer directoryFile.Close()
	return directoryFile.Sync()
}
