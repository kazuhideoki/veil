package infra

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

type ExecTTLCleanerStarter struct{}

func (ExecTTLCleanerStarter) Start() error {
	executablePath, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(executablePath, "ttl-cleaner")
	cmd.Dir = filepath.Dir(executablePath)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}

type FileTTLCleanerLock struct {
	Path string
	file *os.File
}

func (l *FileTTLCleanerLock) Lock() (bool, error) {
	file, err := os.OpenFile(l.Path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false, err
	}

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK {
			return false, nil
		}
		return false, err
	}

	l.file = file
	return true, nil
}

func (l *FileTTLCleanerLock) Unlock() error {
	if l.file == nil {
		return nil
	}

	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		l.file = nil
		return err
	}

	err := l.file.Close()
	l.file = nil
	return err
}
