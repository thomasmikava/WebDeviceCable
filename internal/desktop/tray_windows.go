package desktop

import (
	"fmt"
	"runtime"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	shell32       = windows.NewLazySystemDLL("shell32.dll")
	registerClass = user32.NewProc("RegisterClassExW")
	createWindow  = user32.NewProc("CreateWindowExW")
	defWindowProc = user32.NewProc("DefWindowProcW")
	postMessage   = user32.NewProc("PostMessageW")
	notifyIcon    = shell32.NewProc("Shell_NotifyIconW")
)

const (
	trayClass     = "WebDeviceCable.Tray"
	wmTray        = 0x8001
	wmQuitRequest = 0x8002
	wmClose       = 0x0010
)

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}

type point struct{ X, Y int32 }
type windowMessage struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Point          point
	Private        uint32
}

type notifyIconData struct {
	Size                uint32
	Window              uintptr
	ID, Flags, Callback uint32
	Icon                uintptr
	Tip                 [128]uint16
	State, StateMask    uint32
	Info                [256]uint16
	Version             uint32
	InfoTitle           [64]uint16
	InfoFlags           uint32
	GUID                windows.GUID
	BalloonIcon         uintptr
}

type windowsTray struct {
	id             string
	open, quit     func()
	window         atomic.Uintptr
	icon           uintptr
	taskbarCreated uint32
}

func New(id string, open, quit func()) (Tray, error) {
	return &windowsTray{id: id, open: open, quit: quit}, nil
}

func wide(value string) *uint16 { pointer, _ := windows.UTF16PtrFromString(value); return pointer }

func RequestQuit(id string) error {
	window, _, _ := user32.NewProc("FindWindowW").Call(uintptr(unsafe.Pointer(wide(trayClass))), uintptr(unsafe.Pointer(wide(id))))
	if window == 0 {
		return nil
	}
	ok, _, err := postMessage.Call(window, wmQuitRequest, 0, 0)
	if ok == 0 {
		return fmt.Errorf("request desktop shutdown: %w", err)
	}
	return nil
}

func (t *windowsTray) Run(ready func()) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	instance, _, moduleErr := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW").Call(0)
	if instance == 0 {
		return fmt.Errorf("get executable module: %w", moduleErr)
	}
	class := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Instance: uintptr(instance), Name: wide(trayClass),
		Proc: windows.NewCallback(t.windowProc)}
	atom, _, callErr := registerClass.Call(uintptr(unsafe.Pointer(&class)))
	if atom == 0 {
		return fmt.Errorf("register tray window: %w", callErr)
	}
	defer user32.NewProc("UnregisterClassW").Call(uintptr(unsafe.Pointer(class.Name)), uintptr(instance))
	iconHandle, _, callErr := user32.NewProc("CreateIconFromResourceEx").Call(
		uintptr(unsafe.Pointer(&icon[0])), uintptr(len(icon)), 1, 0x30000, 32, 32, 0)
	runtime.KeepAlive(icon)
	if iconHandle == 0 {
		return fmt.Errorf("load WDC icon: %w", callErr)
	}
	t.icon = iconHandle
	defer user32.NewProc("DestroyIcon").Call(iconHandle)
	message, _, _ := user32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(wide("TaskbarCreated"))))
	t.taskbarCreated = uint32(message)
	// A hidden top-level window receives Explorer's TaskbarCreated broadcast.
	window, _, callErr := createWindow.Call(0, uintptr(unsafe.Pointer(class.Name)), uintptr(unsafe.Pointer(wide(t.id))), 0,
		0, 0, 0, 0, 0, 0, uintptr(instance), 0)
	if window == 0 {
		return fmt.Errorf("create tray window: %w", callErr)
	}
	t.window.Store(window)
	defer t.window.Store(0)
	defer user32.NewProc("DestroyWindow").Call(window)
	defer func() { data := t.notificationData(); notifyIcon.Call(2, uintptr(unsafe.Pointer(&data))) }()
	if err := t.addIcon(); err != nil {
		return err
	}
	ready()
	var event windowMessage
	for {
		result, _, err := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&event)), 0, 0, 0)
		if int32(result) == -1 {
			return fmt.Errorf("tray message loop: %w", err)
		}
		if result == 0 {
			return nil
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&event)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&event)))
	}
}

func (t *windowsTray) notificationData() notifyIconData {
	data := notifyIconData{Size: uint32(unsafe.Sizeof(notifyIconData{})), Window: t.window.Load(), ID: 1,
		Flags: 1 | 2 | 4 | 0x80, Callback: wmTray, Icon: t.icon}
	text, _ := windows.UTF16FromString("WebDeviceCable — click to open")
	copy(data.Tip[:], text)
	return data
}

func (t *windowsTray) addIcon() error {
	data := t.notificationData()
	ok, _, err := notifyIcon.Call(0, uintptr(unsafe.Pointer(&data)))
	if ok == 0 {
		return fmt.Errorf("show WDC tray icon: %w", err)
	}
	data.Version = 4
	notifyIcon.Call(4, uintptr(unsafe.Pointer(&data)))
	return nil
}

func (t *windowsTray) windowProc(window uintptr, message uint32, wParam, lParam uintptr) uintptr {
	if t.taskbarCreated != 0 && message == t.taskbarCreated {
		if err := t.addIcon(); err != nil {
			go t.ShowError(err)
		}
		return 0
	}
	switch message {
	case wmTray:
		switch uint16(lParam) {
		case 0x400, 0x401: // NIN_SELECT / NIN_KEYSELECT (also keyboard activation).
			t.open()
		case 0x007b: // WM_CONTEXTMENU, including keyboard context menus.
			t.contextMenu()
		}
		return 0
	case wmQuitRequest:
		t.quit()
		return 0
	case wmClose:
		user32.NewProc("DestroyWindow").Call(window)
		return 0
	case 0x0002: // WM_DESTROY
		user32.NewProc("PostQuitMessage").Call(0)
		return 0
	}
	result, _, _ := defWindowProc.Call(window, uintptr(message), wParam, lParam)
	return result
}

func (t *windowsTray) contextMenu() {
	menu, _, err := user32.NewProc("CreatePopupMenu").Call()
	if menu == 0 {
		go t.ShowError(err)
		return
	}
	defer user32.NewProc("DestroyMenu").Call(menu)
	appendItem := user32.NewProc("AppendMenuW")
	appendItem.Call(menu, 0, 1, uintptr(unsafe.Pointer(wide("Open WebDeviceCable"))))
	appendItem.Call(menu, 0x800, 0, 0)
	appendItem.Call(menu, 0, 2, uintptr(unsafe.Pointer(wide("Quit WebDeviceCable"))))
	var cursor point
	user32.NewProc("GetCursorPos").Call(uintptr(unsafe.Pointer(&cursor)))
	window := t.window.Load()
	user32.NewProc("SetForegroundWindow").Call(window)
	choice, _, _ := user32.NewProc("TrackPopupMenu").Call(menu, 0x100|0x2, uintptr(cursor.X), uintptr(cursor.Y), 0, window, 0)
	postMessage.Call(window, 0, 0, 0)
	switch choice {
	case 1:
		t.open()
	case 2:
		t.quit()
	}
}

func (t *windowsTray) Close() {
	if window := t.window.Load(); window != 0 {
		postMessage.Call(window, wmClose, 0, 0)
	}
}

func (t *windowsTray) ShowError(err error) {
	user32.NewProc("MessageBoxW").Call(t.window.Load(), uintptr(unsafe.Pointer(wide(err.Error()))),
		uintptr(unsafe.Pointer(wide("WebDeviceCable"))), 0x10)
}
