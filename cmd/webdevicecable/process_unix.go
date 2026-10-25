//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd)  { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func stopSignal() os.Signal { return syscall.SIGTERM }

func reportError(e error) { fmt.Fprintln(os.Stderr, "WebDeviceCable:", e) }
