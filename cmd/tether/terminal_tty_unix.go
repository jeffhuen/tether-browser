//go:build linux || darwin

package main

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"unsafe"
)

type terminalMasterTTY struct {
	file       *os.File
	foreground int32
	master     *exec.Cmd
	changes    chan os.Signal
}

// Initial master authentication may read /dev/tty even when stdin is piped.
// Give only that master the terminal, then restore the calling foreground group
// before launching the normal interactive multiplexed shell.
func terminalMasterForeground(cmd *exec.Cmd) *terminalMasterTTY {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return &terminalMasterTTY{}
	}
	var foreground int32
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, tty.Fd(), syscall.TIOCGPGRP, uintptr(unsafe.Pointer(&foreground)))
	if errno != 0 {
		tty.Close()
		return &terminalMasterTTY{}
	}
	cmd.SysProcAttr.Foreground = true
	cmd.SysProcAttr.Ctty = int(tty.Fd())
	t := &terminalMasterTTY{file: tty, foreground: foreground, master: cmd, changes: make(chan os.Signal, 1)}
	signal.Notify(t.changes, syscall.SIGCHLD)
	return t
}

func (t *terminalMasterTTY) foregroundGroup(group int32) error {
	ignored := signal.Ignored(syscall.SIGTTOU)
	if !ignored {
		signal.Ignore(syscall.SIGTTOU)
		defer signal.Reset(syscall.SIGTTOU)
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, t.file.Fd(), syscall.TIOCSPGRP, uintptr(unsafe.Pointer(&group)))
	if errno != 0 {
		return errno
	}
	return nil
}

func (t *terminalMasterTTY) restore() error {
	if t.file == nil {
		return nil
	}
	signal.Stop(t.changes)
	err := t.foregroundGroup(t.foreground)
	_ = t.file.Close()
	t.file = nil
	return err
}

// waitid requests only stop events, never exits: Cmd.Wait remains the sole
// exit reaper. SIGCHLD wakes this check, so there is no polling or extra waiter.
func terminalChildStopped(pid int) (bool, error) {
	for {
		var info [32]int32 // siginfo_t fits in 128 bytes on Linux and Darwin.
		_, _, errno := syscall.Syscall6(syscall.SYS_WAITID, 1 /* P_PID */, uintptr(pid), uintptr(unsafe.Pointer(&info[0])), syscall.WSTOPPED|syscall.WNOHANG, 0, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno == syscall.ECHILD {
			return false, nil
		}
		if errno != 0 {
			return false, errno
		}
		return info[0] != 0, nil
	}
}

func (t *terminalMasterTTY) suspendIfStopped() (bool, error) {
	if t.file == nil || t.master.Process == nil {
		return false, nil
	}
	stopped, err := terminalChildStopped(t.master.Process.Pid)
	if err != nil || !stopped {
		return false, err
	}
	if err := t.foregroundGroup(t.foreground); err != nil {
		return false, err
	}
	// The shell tracks Tether's job, not the master's separate cancellation
	// group. Stop our job after restoring its terminal; fg resumes us here.
	if err := syscall.Kill(0, syscall.SIGSTOP); err != nil {
		return false, err
	}
	if err := t.foregroundGroup(int32(t.master.Process.Pid)); err != nil {
		return true, err
	}
	return true, syscall.Kill(-t.master.Process.Pid, syscall.SIGCONT)
}
