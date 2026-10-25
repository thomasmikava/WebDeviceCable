package desktop

import (
	"testing"
	"unsafe"
)

func TestWindowsTrayABI(t *testing.T) {
	// Both packaged Windows architectures use the Win64 ABI.
	if got := unsafe.Sizeof(notifyIconData{}); got != 976 {
		t.Fatalf("NOTIFYICONDATAW size: %d", got)
	}
	if got := unsafe.Sizeof(windowClass{}); got != 80 {
		t.Fatalf("WNDCLASSEXW size: %d", got)
	}
	if got := unsafe.Sizeof(windowMessage{}); got != 48 {
		t.Fatalf("MSG size: %d", got)
	}
}
