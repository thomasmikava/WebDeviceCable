package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"os"
	"path/filepath"
	"time"
	"unsafe"
)

func verifyExecutable(path string) error {
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.Mode().IsRegular() {
		return errors.New("update executable is unsafe")
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	f := &windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: p}
	d := &windows.WinTrustData{Size: uint32(unsafe.Sizeof(windows.WinTrustData{})), UIChoice: windows.WTD_UI_NONE, RevocationChecks: windows.WTD_REVOKE_WHOLECHAIN, UnionChoice: windows.WTD_CHOICE_FILE, StateAction: windows.WTD_STATEACTION_VERIFY, FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(f)}
	e = windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, d)
	d.StateAction = windows.WTD_STATEACTION_CLOSE
	_ = windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, d)
	if e != nil {
		return errors.New("Windows could not verify the update publisher signature")
	}
	return nil
}

func replaceUpdateFile(from, to string) error {
	a, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		e = windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
		if e == nil || time.Now().After(deadline) {
			return e
		}
		time.Sleep(50 * time.Millisecond)
	}
}
func installedVersion(parent, version string) error {
	key, e := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Uninstall\WebDeviceCable`, registry.QUERY_VALUE|registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer key.Close()
	path, _, e := key.GetStringValue("InstallLocation")
	if e != nil || filepath.Clean(path) != filepath.Clean(parent) {
		return errors.New("installation registry entry does not match")
	}
	return key.SetStringValue("DisplayVersion", version)
}
