package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/png"
	"log"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	"github.com/godbus/dbus/v5/prop"
)

const (
	itemPath         = dbus.ObjectPath("/StatusNotifierItem")
	menuPath         = dbus.ObjectPath("/MenuBar")
	controlPath      = dbus.ObjectPath("/Desktop")
	itemInterface    = "org.kde.StatusNotifierItem"
	menuInterface    = "com.canonical.dbusmenu"
	controlInterface = "org.webdevicecable.Desktop"
	watcherName      = "org.kde.StatusNotifierWatcher"
)

type linuxTray struct {
	id         string
	open, quit func()
	done       chan struct{}
	once       sync.Once
	conn       *dbus.Conn
}

func New(id string, open, quit func()) (Tray, error) {
	return &linuxTray{id: id, open: open, quit: quit, done: make(chan struct{})}, nil
}

func RequestQuit(id string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return conn.Object(controlInterface+"."+id, controlPath).CallWithContext(ctx, controlInterface+".Quit", 0).Err
}

func (t *linuxTray) Run(ready func()) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to the desktop session bus: %w", err)
	}
	t.conn = conn
	defer conn.Close()
	name := controlInterface + "." + t.id
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil {
		return err
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return errors.New("WebDeviceCable already has a tray icon")
	}
	pixmap, err := trayPixmap()
	if err != nil {
		return err
	}
	properties := prop.Map{}
	for _, iface := range []string{itemInterface, "org.freedesktop.StatusNotifierItem"} {
		properties[iface] = map[string]*prop.Prop{}
		values := map[string]any{
			"Category": "ApplicationStatus", "Id": "webdevicecable", "Title": "WebDeviceCable",
			"Status": "Active", "WindowId": uint32(0), "IconName": "", "IconPixmap": []iconPixmap{pixmap},
			"OverlayIconName": "", "OverlayIconPixmap": []iconPixmap{},
			"AttentionIconName": "", "AttentionIconPixmap": []iconPixmap{}, "AttentionMovieName": "",
			"ToolTip": struct {
				Icon               string
				Pixmaps            []iconPixmap
				Title, Description string
			}{
				"", []iconPixmap{pixmap}, "WebDeviceCable", "Open the WebDeviceCable control panel",
			},
			"ItemIsMenu": false, "Menu": menuPath,
		}
		for key, value := range values {
			properties[iface][key] = &prop.Prop{Value: value, Emit: prop.EmitConst}
		}
	}
	props, err := prop.Export(conn, itemPath, properties)
	if err != nil {
		return err
	}
	item := &linuxItem{tray: t}
	for iface := range properties {
		if err := conn.Export(item, itemPath, iface); err != nil {
			return err
		}
	}
	itemIntro := &introspect.Node{Name: string(itemPath), Interfaces: []introspect.Interface{introspect.IntrospectData, prop.IntrospectData}}
	for iface := range properties {
		itemIntro.Interfaces = append(itemIntro.Interfaces, introspect.Interface{Name: iface, Methods: introspect.Methods(item), Properties: props.Introspection(iface)})
	}
	if err := conn.Export(introspect.NewIntrospectable(itemIntro), itemPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return err
	}
	menu := &linuxMenu{tray: t}
	if err := conn.Export(menu, menuPath, menuInterface); err != nil {
		return err
	}
	menuProps, err := prop.Export(conn, menuPath, prop.Map{menuInterface: {
		"Version": {Value: uint32(3)}, "TextDirection": {Value: "ltr"}, "Status": {Value: "normal"}, "IconThemePath": {Value: []string{}},
	}})
	if err != nil {
		return err
	}
	if err := conn.Export(introspect.NewIntrospectable(&introspect.Node{Interfaces: []introspect.Interface{
		introspect.IntrospectData, prop.IntrospectData,
		{Name: menuInterface, Methods: introspect.Methods(menu), Properties: menuProps.Introspection(menuInterface)},
	}}), menuPath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return err
	}
	if err := conn.Export(&linuxControl{tray: t}, controlPath, controlInterface); err != nil {
		return err
	}
	signals := make(chan *dbus.Signal, 8)
	conn.Signal(signals)
	defer conn.RemoveSignal(signals)
	if err := conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.DBus"), dbus.WithMatchMember("NameOwnerChanged"), dbus.WithMatchArg(0, watcherName)); err != nil {
		return err
	}
	// Register again when the panel restarts or tray support becomes available.
	register := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return conn.Object(watcherName, "/StatusNotifierWatcher").CallWithContext(ctx, watcherName+".RegisterStatusNotifierItem", 0, name).Err
	}
	if err := register(); err != nil {
		log.Printf("WebDeviceCable tray host is unavailable: %v", err)
		t.ShowError(errors.New("Your desktop has no active system tray. Open WebDeviceCable from the applications menu, or enable StatusNotifier/AppIndicator support"))
	}
	ready()
	for {
		select {
		case <-t.done:
			return nil
		case <-conn.Context().Done():
			return errors.New("desktop session bus disconnected")
		case signal := <-signals:
			if signal != nil && len(signal.Body) == 3 && signal.Body[2] != "" {
				if err := register(); err != nil {
					log.Printf("WebDeviceCable tray registration: %v", err)
				}
			}
		}
	}
}

func (t *linuxTray) Close() { t.once.Do(func() { close(t.done) }) }

func (t *linuxTray) ShowError(err error) {
	log.Printf("WebDeviceCable: %v", err)
	if t.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = t.conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").CallWithContext(ctx,
		"org.freedesktop.Notifications.Notify", 0, "WebDeviceCable", uint32(0), "", "WebDeviceCable", err.Error(), []string{}, map[string]dbus.Variant{}, int32(10000)).Err
}

type iconPixmap struct {
	Width, Height int32
	Pixels        []byte
}

func trayPixmap() (iconPixmap, error) {
	img, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		return iconPixmap{}, err
	}
	bounds := img.Bounds()
	result := iconPixmap{Width: int32(bounds.Dx()), Height: int32(bounds.Dy()), Pixels: make([]byte, 0, bounds.Dx()*bounds.Dy()*4)}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			result.Pixels = append(result.Pixels, byte(a>>8), byte(r>>8), byte(g>>8), byte(b>>8))
		}
	}
	return result, nil
}

type linuxItem struct{ tray *linuxTray }

func (i *linuxItem) Activate(_, _ int32) *dbus.Error          { i.tray.open(); return nil }
func (i *linuxItem) SecondaryActivate(_, _ int32) *dbus.Error { i.tray.open(); return nil }

// SNI hosts render the exported dbusmenu for a context click.
func (i *linuxItem) ContextMenu(_, _ int32) *dbus.Error             { return nil }
func (i *linuxItem) Scroll(_ int32, _ string) *dbus.Error           { return nil }
func (i *linuxItem) ProvideXdgActivationToken(_ string) *dbus.Error { return nil }

type linuxControl struct{ tray *linuxTray }

func (c *linuxControl) Quit() *dbus.Error { c.tray.quit(); return nil }

type menuLayout struct {
	ID         int32
	Properties map[string]dbus.Variant
	Children   []dbus.Variant
}
type menuProperties struct {
	ID         int32
	Properties map[string]dbus.Variant
}
type menuEvent struct {
	ID        int32
	Event     string
	Data      dbus.Variant
	Timestamp uint32
}
type linuxMenu struct{ tray *linuxTray }

func menuItem(id int32, names []string) (menuLayout, bool) {
	values := map[string]any{"enabled": true, "visible": true}
	switch id {
	case 0:
		values["children-display"] = "submenu"
	case 1:
		values["label"] = "Open WebDeviceCable"
	case 2:
		values["type"] = "separator"
	case 3:
		values["label"] = "Quit WebDeviceCable"
	default:
		return menuLayout{}, false
	}
	item := menuLayout{ID: id, Properties: map[string]dbus.Variant{}, Children: []dbus.Variant{}}
	for key, value := range values {
		include := len(names) == 0
		for _, name := range names {
			if name == key {
				include = true
			}
		}
		if include {
			item.Properties[key] = dbus.MakeVariant(value)
		}
	}
	return item, true
}

func (m *linuxMenu) GetLayout(parent, depth int32, names []string) (uint32, menuLayout, *dbus.Error) {
	layout, ok := menuItem(parent, names)
	if !ok {
		return 0, layout, dbus.MakeFailedError(errors.New("unknown menu item"))
	}
	if parent == 0 && depth != 0 {
		for _, id := range []int32{1, 2, 3} {
			child, _ := menuItem(id, names)
			layout.Children = append(layout.Children, dbus.MakeVariant(child))
		}
	}
	return 1, layout, nil
}

func (m *linuxMenu) GetGroupProperties(ids []int32, names []string) ([]menuProperties, *dbus.Error) {
	if len(ids) == 0 {
		ids = []int32{0, 1, 2, 3}
	}
	result := []menuProperties{}
	for _, id := range ids {
		if item, ok := menuItem(id, names); ok {
			result = append(result, menuProperties{ID: id, Properties: item.Properties})
		}
	}
	return result, nil
}

func (m *linuxMenu) GetProperty(id int32, name string) (dbus.Variant, *dbus.Error) {
	item, _ := menuItem(id, []string{name})
	value, ok := item.Properties[name]
	if !ok {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("unknown menu property"))
	}
	return value, nil
}

func (m *linuxMenu) Event(id int32, event string, _ dbus.Variant, _ uint32) *dbus.Error {
	if event == "clicked" {
		switch id {
		case 1:
			m.tray.open()
		case 3:
			m.tray.quit()
		}
	}
	return nil
}

func (m *linuxMenu) EventGroup(events []menuEvent) ([]int32, *dbus.Error) {
	for _, event := range events {
		m.Event(event.ID, event.Event, event.Data, event.Timestamp)
	}
	return []int32{}, nil
}
func (m *linuxMenu) AboutToShow(_ int32) (bool, *dbus.Error) { return false, nil }
func (m *linuxMenu) AboutToShowGroup(_ []int32) ([]int32, []int32, *dbus.Error) {
	return []int32{}, []int32{}, nil
}
