package desktop

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

type testWatcher struct{ registered chan string }

func (w *testWatcher) RegisterStatusNotifierItem(name string) *dbus.Error {
	w.registered <- name
	return nil
}

func trayReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("tray event timed out")
		var zero T
		return zero
	}
}

func privateBus(t *testing.T) *dbus.Conn {
	t.Helper()
	path, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon is required for the private-session tray test")
	}
	process := exec.Command(path, "--session", "--nofork", "--print-address=1")
	process.Stderr = os.Stderr
	output, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() {
		t.Fatal("private D-Bus did not publish an address")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", scanner.Text())
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestLinuxTrayOverPrivateSessionBus(t *testing.T) {
	conn := privateBus(t)
	watcher := &testWatcher{registered: make(chan string, 4)}
	if err := conn.Export(watcher, "/StatusNotifierWatcher", watcherName); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(watcherName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	opened, quit := make(chan struct{}, 4), make(chan struct{}, 4)
	id := InstanceID(t.TempDir())
	tray, err := New(id, func() { opened <- struct{}{} }, func() { quit <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	defer tray.Close()
	ready, finished := make(chan struct{}), make(chan error, 1)
	go func() { finished <- tray.Run(func() { close(ready) }) }()
	name := trayReceive(t, watcher.registered)
	trayReceive(t, ready)
	item := conn.Object(name, itemPath)
	menu := conn.Object(name, menuPath)
	var isMenu dbus.Variant
	if err := item.Call("org.freedesktop.DBus.Properties.Get", 0, itemInterface, "ItemIsMenu").Store(&isMenu); err != nil {
		t.Fatal(err)
	}
	if isMenu.Value() != false {
		t.Fatal("tray hosts must be allowed to activate the icon directly")
	}
	var revision uint32
	var layout menuLayout
	if err := menu.Call(menuInterface+".GetLayout", 0, int32(0), int32(-1), []string{}).Store(&revision, &layout); err != nil {
		t.Fatal(err)
	}
	if revision != 1 || len(layout.Children) != 3 {
		t.Fatalf("menu layout: %#v", layout)
	}
	var openItem menuLayout
	if err := dbus.Store([]any{layout.Children[0].Value()}, &openItem); err != nil {
		t.Fatal(err)
	}
	if openItem.Properties["label"].Value() != "Open WebDeviceCable" {
		t.Fatal("missing Open menu action")
	}
	if err := item.Call(itemInterface+".Activate", 0, int32(0), int32(0)).Err; err != nil {
		t.Fatal(err)
	}
	trayReceive(t, opened)
	if err := menu.Call(menuInterface+".Event", 0, int32(1), "clicked", dbus.MakeVariant(int32(0)), uint32(0)).Err; err != nil {
		t.Fatal(err)
	}
	trayReceive(t, opened)
	if err := menu.Call(menuInterface+".Event", 0, int32(3), "clicked", dbus.MakeVariant(int32(0)), uint32(0)).Err; err != nil {
		t.Fatal(err)
	}
	trayReceive(t, quit)
	// A panel restart must restore the existing icon without restarting WDC.
	if _, err := conn.ReleaseName(watcherName); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(watcherName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	if got := trayReceive(t, watcher.registered); got != name {
		t.Fatal("registered a different icon after panel restart")
	}
	if err := RequestQuit(id); err != nil {
		t.Fatal(err)
	}
	trayReceive(t, quit)
	tray.Close()
	if err := trayReceive(t, finished); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxDesktopExecutableLifecycle(t *testing.T) {
	binary := os.Getenv("WDC_DESKTOP_BINARY")
	if binary == "" {
		t.Skip("set WDC_DESKTOP_BINARY for the real desktop/runner lifecycle check")
	}
	conn := privateBus(t)
	watcher := &testWatcher{registered: make(chan string, 4)}
	if err := conn.Export(watcher, "/StatusNotifierWatcher", watcherName); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.RequestName(watcherName, dbus.NameFlagDoNotQueue); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("bridge:\n  disableNative: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Replace only the browser opener, so no real browser or native account is used.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "xdg-open"), []byte("#!/bin/sh\nprintf '%s' \"$1\" > \"$WDC_TEST_BROWSER\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	opened := filepath.Join(dir, "browser-opened")
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("WDC_TEST_BROWSER", opened)
	defer func() { _ = exec.Command(binary, "stop", "--data-dir", dir, "--if-running").Run() }()
	args := []string{"desktop", "--data-dir", dir, "--port", "0", "--no-open"}
	process := exec.Command(binary, args...)
	process.Stdout, process.Stderr = io.Discard, io.Discard
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer process.Process.Kill()
	finished := make(chan error, 1)
	go func() { finished <- process.Wait() }()
	name := trayReceive(t, watcher.registered)
	waitFile := func(path string) []byte {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); {
			if data, err := os.ReadFile(path); err == nil {
				return data
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("desktop did not produce expected state")
		return nil
	}
	var runtime struct {
		URL string `json:"url"`
		PID int    `json:"pid"`
	}
	if err := json.Unmarshal(waitFile(filepath.Join(dir, "runner.json")), &runtime); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(binary, args...).Run(); err != nil {
		t.Fatal("reopen:", err)
	}
	var reused struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(waitFile(filepath.Join(dir, "runner.json")), &reused); err != nil {
		t.Fatal(err)
	}
	if reused.PID != runtime.PID {
		t.Fatal("reopening replaced the runner")
	}
	select {
	case duplicate := <-watcher.registered:
		t.Fatal("duplicate tray:", duplicate)
	default:
	}
	if err := conn.Object(name, itemPath).Call(itemInterface+".Activate", 0, int32(0), int32(0)).Err; err != nil {
		t.Fatal(err)
	}
	if link := string(waitFile(opened)); !strings.HasPrefix(link, runtime.URL+"/#key=") {
		t.Fatal("icon did not open the authenticated web panel")
	}
	if err := exec.Command(binary, "desktop-stop", "--data-dir", dir).Run(); err != nil {
		t.Fatal("installer shutdown:", err)
	}
	if err := trayReceive(t, finished); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(filepath.Join(dir, "runner.json")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(end) {
			t.Fatal("Quit did not stop the runner")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
