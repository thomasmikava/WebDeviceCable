// Package desktop provides native tray access; the product UI stays in the browser.
package desktop

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
)

//go:embed icon.png
var icon []byte

type Tray interface {
	Run(ready func()) error
	Close()
	ShowError(error)
}

func InstanceID(dir string) string {
	return fmt.Sprintf("d%x", sha256.Sum256([]byte(dir)))
}
