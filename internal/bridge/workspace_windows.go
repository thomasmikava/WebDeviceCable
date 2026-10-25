package bridge

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func rootIdentity(root *os.Root) (string, error) {
	f, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer f.Close()
	var info windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
func openWorkspaceFile(root *os.Root, path string) (*os.File, error) { return root.Open(path) }
