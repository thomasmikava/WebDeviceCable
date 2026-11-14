package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// LoginItem is injected by the runner; tests never alter this user's login items.
type LoginItem interface {
	Status() (bool, error)
	Set(bool) error
}
type LoginStatus struct {
	Supported bool   `json:"supported"`
	Enabled   bool   `json:"enabled"`
	Reason    string `json:"reason,omitempty"`
}
type fileLoginItem struct{ path, content string }

func (l fileLoginItem) Status() (bool, error) {
	b, e := os.ReadFile(l.path)
	if os.IsNotExist(e) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	return string(b) == l.content, nil
}
func (l fileLoginItem) Set(on bool) error {
	if !on {
		e := os.Remove(l.path)
		if os.IsNotExist(e) {
			return nil
		}
		return e
	}
	if e := os.MkdirAll(filepath.Dir(l.path), 0700); e != nil {
		return e
	}
	return os.WriteFile(l.path, []byte(l.content), 0600)
}

// DetectLoginItem only reads paths. It never registers or removes anything.
// Source builds and alternate data directories cannot change desktop startup.
func DetectLoginItem(dataDir string) (LoginItem, string) {
	exe, e := os.Executable()
	if e != nil {
		return nil, "The installed desktop app could not be located."
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return nil, "The user profile could not be located."
	}
	config, e := os.UserConfigDir()
	if e != nil {
		return nil, "The user configuration directory could not be located."
	}
	expected := filepath.Join(config, "WebDeviceCable")
	if filepath.Clean(dataDir) != filepath.Clean(expected) {
		return nil, "Start at login is available only in the installed desktop app with its default data directory."
	}
	return installedLoginItem(runtime.GOOS, exe, home, os.Getenv("LOCALAPPDATA"), os.Getenv("APPDATA"), os.Getenv("XDG_CONFIG_HOME"))
}
func installedLoginItem(platform, exe, home, local, roaming, xdg string) (LoginItem, string) {
	switch platform {
	case "linux":
		// Require the installer-created desktop entry and exact executable path.
		data := os.Getenv("XDG_DATA_HOME")
		if data == "" {
			data = filepath.Join(home, ".local", "share")
		}
		entry, e := os.ReadFile(filepath.Join(data, "applications", "webdevicecable.desktop"))
		quoted := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%", "`", "\\`", "$", "\\$").Replace(exe)
		if e != nil || !strings.Contains(string(entry), `Exec="`+quoted+`" desktop %u`) {
			return nil, "Install the desktop app to enable start at login."
		}
		if xdg == "" {
			xdg = filepath.Join(home, ".config")
		}
		return fileLoginItem{filepath.Join(xdg, "autostart", "webdevicecable.desktop"), fmt.Sprintf("[Desktop Entry]\nType=Application\nName=WebDeviceCable\nExec=\"%s\" desktop --no-open\nTerminal=false\n", quoted)}, ""
	case "windows":
		if local == "" || roaming == "" {
			return nil, "The Windows user profile is unavailable."
		}
		dir := filepath.Join(local, "Programs", "WebDeviceCable")
		launcher := filepath.Join(dir, "WebDeviceCable Launcher.exe")
		if !strings.EqualFold(filepath.Clean(exe), filepath.Join(dir, "WebDeviceCable.exe")) {
			return nil, "Install the desktop app to enable start at login."
		}
		if _, e := os.Stat(launcher); e != nil {
			return nil, "The desktop launcher is unavailable."
		}
		// Startup runs through the installed GUI launcher, with no console or panel.
		if strings.ContainsAny(launcher, "\r\n\"%") {
			return nil, "This installation path cannot be used for startup."
		}
		return fileLoginItem{filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "Startup", "WebDeviceCable.vbs"), "CreateObject(\"WScript.Shell\").Run \"\"\"" + launcher + "\"\" desktop --no-open\", 0, False\r\n"}, ""
	case "darwin":
		return nil, "The macOS menu bar launcher has no bundled login helper or ServiceManagement integration; start at login is unavailable."
	}
	return nil, "Start at login is unavailable on this platform."
}
func LoginItemStatus(item LoginItem, reason string) LoginStatus {
	s := LoginStatus{Supported: item != nil, Reason: reason}
	if item != nil {
		on, e := item.Status()
		s.Enabled = on
		if e != nil {
			s.Supported = false
			s.Reason = e.Error()
		}
	}
	return s
}
func SetLoginItem(item LoginItem, on bool) error {
	if item == nil {
		return errors.New("start at login is unavailable")
	}
	return item.Set(on)
}
