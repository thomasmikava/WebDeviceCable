package bridge

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		sddl := sd.String()
		if !strings.Contains(sddl, "D:P") || !strings.Contains(sddl, user.User.Sid.String()) || strings.Contains(sddl, ";;;WD)") || strings.Contains(sddl, ";;;BU)") || strings.Contains(sddl, ";;;AU)") {
			t.Fatalf("unprotected DACL: %s", sddl)
		}
	}
	// New runtime children receive the private parent's inherited access.
	child := filepath.Join(dir, "inherited")
	if err := os.WriteFile(child, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(child, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sd.String(), user.User.Sid.String()) || strings.Contains(sd.String(), ";;;BU)") {
		t.Fatalf("bad inherited DACL: %s", sd.String())
	}
}
