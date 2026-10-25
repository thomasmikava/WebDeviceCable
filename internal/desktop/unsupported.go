//go:build !linux && !windows

package desktop

import "errors"

func New(_ string, _, _ func()) (Tray, error) {
	return nil, errors.New("open WebDeviceCable.app to use the macOS menu bar icon")
}

func RequestQuit(_ string) error { return nil }
