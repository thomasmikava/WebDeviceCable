package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/thomasmikava/WebDeviceCable/internal/bridge"
	"github.com/thomasmikava/WebDeviceCable/internal/desktop"
)

func runDesktop(args []string) error {
	return desktopSession(args, desktop.New, run)
}

func desktopSession(args []string, newTray func(string, func(), func()) (desktop.Tray, error), command func([]string) error) error {
	if len(args) > 0 && args[0] == "launch" {
		args = args[1:]
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("WebDeviceCable desktop", flag.ContinueOnError)
	dir := flags.String("data-dir", defaultDataDirectory(root), "Private state directory")
	port := flags.Int("port", 47321, "Loopback port (0 selects an unused port)")
	noOpen := flags.Bool("no-open", false, "Do not open the control panel on startup")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *port < 0 || *port > 65535 || flags.NArg() > 1 {
		return errors.New("desktop expects an optional connection URL and a port from 0–65535")
	}
	uri := ""
	if flags.NArg() == 1 {
		uri = flags.Arg(0)
		if _, err := bridge.ParseLaunchURI(uri); err != nil {
			return err
		}
	}
	*dir, err = filepath.Abs(*dir)
	if err != nil {
		return err
	}
	if err := bridge.PrepareDataDirectory(*dir); err != nil {
		return err
	}
	start := []string{"start", "--data-dir", *dir, "--port", fmt.Sprint(*port)}
	initial := append([]string{}, start...)
	if *noOpen {
		initial = append(initial, "--no-open")
	}
	if uri != "" {
		initial = []string{"launch", uri, "--data-dir", *dir, "--port", fmt.Sprint(*port)}
	}
	lock := flock.New(filepath.Join(*dir, "desktop.lock"))
	defer lock.Close()
	locked, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		// Reopening the app uses the same runner and leaves its one icon alone.
		return command(initial)
	}
	open, quit := make(chan struct{}, 1), make(chan struct{}, 1)
	enqueue := func(ch chan struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
	tray, err := newTray(desktop.InstanceID(*dir), func() { enqueue(open) }, func() { enqueue(quit) })
	if err != nil {
		return err
	}
	defer tray.Close()
	done := make(chan struct{})
	var worker sync.WaitGroup
	err = tray.Run(func() {
		worker.Add(1)
		go func() {
			defer worker.Done()
			if err := command(initial); err != nil {
				tray.ShowError(err)
			}
			for {
				select {
				case <-done:
					return
				case <-open:
					if err := command(start); err != nil {
						tray.ShowError(err)
					}
				case <-quit:
					if err := command([]string{"stop", "--data-dir", *dir, "--if-running"}); err != nil {
						tray.ShowError(err)
						continue
					}
					tray.Close()
					return
				}
			}
		}()
	})
	close(done)
	worker.Wait()
	return err
}

func stopDesktop(dir string) error {
	lock := flock.New(filepath.Join(dir, "desktop.lock"))
	defer lock.Close()
	if locked, err := lock.TryLock(); err != nil {
		return err
	} else if locked {
		return nil
	}
	// Retry during startup, then wait until the native executable is no longer in use.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		_ = desktop.RequestQuit(desktop.InstanceID(dir))
		if locked, err := lock.TryLock(); err != nil {
			return err
		} else if locked {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("WebDeviceCable desktop app did not quit; close it before updating")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
