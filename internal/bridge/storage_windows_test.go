package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateStorageDACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := PrepareDataDirectory(dir); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "secret")
	if err := writeLocalFile(file, []byte("private")); err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, file} {
		assertPrivateWindowsDACL(t, path, user.User.Sid, true)
	}
	// New runtime children receive the private parent's inherited access.
	child := filepath.Join(dir, "inherited")
	if err := os.WriteFile(child, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	assertPrivateWindowsDACL(t, child, user.User.Sid, false)
}

func assertPrivateWindowsDACL(t *testing.T, path string, owner *windows.SID, protected bool) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if protected && control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("unprotected DACL: %s", sd.String())
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		t.Fatalf("missing private DACL: %v", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	var hasOwner, hasSystem bool
	for index := uint32(0); index < uint32(acl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, index, &ace); err != nil {
			t.Fatal(err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("unexpected private ACE: %s", sd.String())
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		isOwner, isSystem := sid.Equals(owner), sid.Equals(system)
		if !isOwner && !isSystem {
			t.Fatalf("DACL allows another account: %s", sd.String())
		}
		hasOwner = hasOwner || isOwner
		hasSystem = hasSystem || isSystem
	}
	if !hasOwner || !hasSystem {
		t.Fatalf("DACL must allow the account and SYSTEM: %s", sd.String())
	}
}
