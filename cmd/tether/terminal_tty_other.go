//go:build !linux && !darwin

package main

import (
	"os"
	"os/exec"
)

type terminalMasterTTY struct {
	changes chan os.Signal
}

func terminalMasterForeground(*exec.Cmd) *terminalMasterTTY { return &terminalMasterTTY{} }
func (*terminalMasterTTY) restore() error                   { return nil }
func (*terminalMasterTTY) suspendIfStopped() (bool, error)  { return false, nil }
