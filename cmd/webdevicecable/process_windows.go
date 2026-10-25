package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200}
}
func stopSignal() os.Signal { return os.Interrupt }

func reportError(e error) {
	fmt.Fprintln(os.Stderr, "WebDeviceCable:", e)
	if gui == "true" {
		title, _ := syscall.UTF16PtrFromString("WebDeviceCable")
		message, _ := syscall.UTF16PtrFromString(e.Error())
		syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
	}
}
