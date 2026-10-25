package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/desktop"
)

type fakeTray struct {
	open, quit func()
	done       chan struct{}
	errors     chan error
	once       sync.Once
}

func (f *fakeTray) Run(ready func()) error { ready(); <-f.done; return nil }
func (f *fakeTray) Close()                 { f.once.Do(func() { close(f.done) }) }
func (f *fakeTray) ShowError(err error)    { f.errors <- err }

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("desktop action timed out")
		var zero T
		return zero
	}
}

func TestDesktopKeepsOneIconReopensAndStops(t *testing.T) {
	dir := t.TempDir()
	tray := &fakeTray{done: make(chan struct{}), errors: make(chan error, 4)}
	defer tray.Close()
	commands := make(chan []string, 8)
	var failOpen, failQuit atomic.Bool
	command := func(args []string) error {
		commands <- append([]string{}, args...)
		if args[0] == "start" && failOpen.Swap(false) {
			return errors.New("cannot open browser")
		}
		if args[0] == "stop" && failQuit.Swap(false) {
			return errors.New("runner rejected shutdown")
		}
		return nil
	}
	factory := func(id string, open, quit func()) (desktop.Tray, error) {
		if id != desktop.InstanceID(dir) {
			t.Errorf("wrong instance scope: %s", id)
		}
		tray.open, tray.quit = open, quit
		return tray, nil
	}
	finished := make(chan error, 1)
	go func() {
		finished <- desktopSession([]string{"--data-dir", dir, "--port", "0", "--no-open"}, factory, command)
	}()
	initial := receive(t, commands)
	if initial[0] != "start" || !slices.Contains(initial, "--no-open") {
		t.Fatalf("startup: %v", initial)
	}
	// A second app launch must open the panel without constructing another tray.
	if err := desktopSession([]string{"--data-dir", dir}, func(string, func(), func()) (desktop.Tray, error) {
		t.Error("created a duplicate icon")
		return nil, errors.New("duplicate")
	}, command); err != nil {
		t.Fatal(err)
	}
	if args := receive(t, commands); args[0] != "start" || slices.Contains(args, "--no-open") {
		t.Fatalf("reopen: %v", args)
	}
	failOpen.Store(true)
	tray.open()
	receive(t, commands)
	if err := receive(t, tray.errors); err.Error() != "cannot open browser" {
		t.Fatal(err)
	}
	tray.open()
	if args := receive(t, commands); args[0] != "start" || slices.Contains(args, "--no-open") {
		t.Fatalf("icon click: %v", args)
	}
	// A failed shutdown leaves the icon available for retry.
	failQuit.Store(true)
	tray.quit()
	receive(t, commands)
	receive(t, tray.errors)
	select {
	case <-tray.done:
		t.Fatal("removed icon after failed shutdown")
	default:
	}
	tray.quit()
	if args := receive(t, commands); args[0] != "stop" || !slices.Contains(args, "--if-running") {
		t.Fatalf("quit: %v", args)
	}
	if err := receive(t, finished); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopRejectsInvalidLaunchBeforeCreatingState(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	for _, args := range [][]string{{"--data-dir", dir, "https://example.com"}, {"--data-dir", dir, "--port", "65536"}} {
		if err := desktopSession(args, func(string, func(), func()) (desktop.Tray, error) {
			t.Fatal("created native UI for invalid launch")
			return nil, nil
		}, func([]string) error { t.Fatal("executed invalid launch"); return nil }); err == nil {
			t.Fatal("accepted invalid launch")
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid launch created state")
	}
	if err := run([]string{"launch", "", "--data-dir", dir}); err == nil {
		t.Fatal("accepted an empty connection URL")
	}
}

func TestStopIfRunningDoesNotTrustMalformedRuntime(t *testing.T) {
	dir := t.TempDir()
	args := []string{"stop", "--data-dir", dir, "--if-running"}
	if err := run(args); err != nil {
		t.Fatal("already stopped:", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runner.json"), []byte("invalid runtime"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(args); err == nil {
		t.Fatal("ignored an invalid runtime")
	}
}
