package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginItemAbstractionPersistence(t *testing.T) {
	// The only writes in this test are to its disposable directory.
	item := fileLoginItem{path: filepath.Join(t.TempDir(), "autostart", "fixture.desktop"), content: "fixture startup"}
	if s := LoginItemStatus(item, ""); !s.Supported || s.Enabled {
		t.Fatal(s)
	}
	if e := SetLoginItem(item, true); e != nil {
		t.Fatal(e)
	}
	if s := LoginItemStatus(fileLoginItem{item.path, item.content}, ""); !s.Enabled {
		t.Fatal(s)
	}
	if e := SetLoginItem(item, false); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(item.path); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if e := SetLoginItem(item, false); e != nil {
		t.Fatal(e)
	}
}
func TestLoginPlatformsAndInstalledPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "share"))
	if item, why := installedLoginItem("darwin", "/fixture/app", home, "", "", ""); item != nil || why == "" {
		t.Fatal("macOS must explain missing helper")
	}
	if item, _ := installedLoginItem("linux", "/fixture/app", home, "", "", ""); item != nil {
		t.Fatal("source build offered login item")
	}
	exe := filepath.Join(home, "bin", "WebDeviceCable")
	entry := filepath.Join(home, "share", "applications", "webdevicecable.desktop")
	os.MkdirAll(filepath.Dir(entry), 0700)
	// Desktop entries escape backslashes, which Windows temp paths contain.
	os.WriteFile(entry, []byte("Exec=\""+strings.ReplaceAll(exe, `\`, `\\`)+"\" desktop %u\n"), 0600)
	item, why := installedLoginItem("linux", exe, home, "", "", filepath.Join(home, "config"))
	if item == nil || why != "" {
		t.Fatal(why)
	}
	if _, e := os.Stat(filepath.Join(home, "config", "autostart")); !os.IsNotExist(e) {
		t.Fatal("detection wrote a login item")
	}
	// Exercise Windows path generation with fixture paths on any host.
	local := filepath.Join(home, "local")
	roaming := filepath.Join(home, "roaming")
	dir := filepath.Join(local, "Programs", "WebDeviceCable")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "WebDeviceCable Launcher.exe"), nil, 0600)
	item, why = installedLoginItem("windows", filepath.Join(dir, "WebDeviceCable.exe"), home, local, roaming, "")
	if item == nil || why != "" {
		t.Fatal(why)
	}
	if _, e := os.Stat(roaming); !os.IsNotExist(e) {
		t.Fatal("Windows detection wrote a login item")
	}
}
