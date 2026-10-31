package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/bridge"
)

var version = "dev"

var gui = "false"

func main() {
	args := os.Args[1:]
	if gui == "true" && (len(args) == 0 || args[0] == "launch") {
		args = append([]string{"desktop"}, args...)
	}
	if e := run(args); e != nil {
		reportError(e)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) > 0 && args[0] == "update" {
		return updateCLI(args[1:])
	}
	if len(args) > 0 && args[0] == "internal-update" {
		return applyUpdate(args[1:])
	}
	if len(args) > 0 && args[0] == "desktop" {
		return runDesktop(args[1:])
	}
	if len(args) == 1 && args[0] == "launch" {
		args = nil
	}
	launchURI := ""
	if len(args) >= 2 && args[0] == "launch" {
		if _, err := bridge.ParseLaunchURI(args[1]); err != nil {
			return err
		}
		launchURI = args[1]
		args = append([]string{"start"}, args[2:]...)
	}
	if len(args) == 1 && args[0] == "internal-mcp" {
		return bridge.RunMCPHelper()
	}
	command := "start"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	if command == "version" {
		fmt.Println(version)
		return nil
	}
	root, e := os.UserConfigDir()
	if e != nil {
		return e
	}
	flags := flag.NewFlagSet("WebDeviceCable "+command, flag.ContinueOnError)
	dir := flags.String("data-dir", defaultDataDirectory(root), "Private state directory")
	port := flags.Int("port", 47321, "Loopback port (0 selects an unused port)")
	noOpen := flags.Bool("no-open", false, "Do not open the control panel")
	printLink := flags.Bool("print", false, "Print the private control-panel link")
	ifRunning := flags.Bool("if-running", false, "Succeed when stopping an already stopped runner")
	if e = flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments")
	}
	if *port < 0 || *port > 65535 {
		return errors.New("port must be 0–65535")
	}
	*dir, e = filepath.Abs(*dir)
	if e != nil {
		return e
	}
	if launchURI != "" {
		return launchConnectionAt(launchURI, *dir, *port)
	}
	switch command {
	case "desktop-stop":
		if _, e := os.Stat(*dir); errors.Is(e, os.ErrNotExist) {
			return nil
		}
		return stopDesktop(*dir)
	case "serve":
		a, e := bridge.Open(*dir)
		if e != nil {
			return e
		}
		defer a.Close()
		listener, e := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", *port))
		if e != nil {
			return e
		}
		if e = configuredUpdates(a, *dir, listener.Addr().(*net.TCPAddr).Port); e != nil {
			listener.Close()
			return e
		}
		fmt.Println("WebDeviceCable listening on http://" + listener.Addr().String())
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, stopSignal())
		defer cancel()
		return a.Serve(ctx, listener)
	case "start":
		info, e := readRuntime(*dir)
		if e != nil || !alive(*dir, info) {
			if e = bridge.PrepareDataDirectory(*dir); e != nil {
				return e
			}
			exe, e := os.Executable()
			if e != nil {
				return e
			}
			logPath := filepath.Join(*dir, "runner.log")
			if _, err := bridge.ReadLocalFile(logPath, 16<<20); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			log, e := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if e != nil {
				return e
			}
			defer log.Close()
			child := exec.Command(exe, "serve", "--data-dir", *dir, "--port", fmt.Sprint(*port))
			child.Stdout = log
			child.Stderr = log
			detach(child)
			if e = child.Start(); e != nil {
				return e
			}
			child.Process.Release()
			ready := false
			for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
				time.Sleep(100 * time.Millisecond)
				info, e = readRuntime(*dir)
				if e == nil && alive(*dir, info) {
					ready = true
					break
				}
			}
			if !ready {
				return fmt.Errorf("runner could not start; inspect %s", filepath.Join(*dir, "runner.log"))
			}
		}
		if *noOpen {
			fmt.Println("WebDeviceCable is running at", info.URL)
			return nil
		}
		return openPanel(*dir, info, *printLink)
	case "open":
		info, e := readRuntime(*dir)
		if e != nil || !alive(*dir, info) {
			return errors.New("runner is not running; use WebDeviceCable start")
		}
		return openPanel(*dir, info, *printLink)
	case "stop":
		info, e := readRuntime(*dir)
		if *ifRunning && errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil || !alive(*dir, info) {
			return errors.New("runner is not running or could not be authenticated")
		}
		key, e := bridge.ReadLocalFile(filepath.Join(*dir, "admin.key"), 64)
		if e != nil {
			return e
		}
		req, e := http.NewRequest("POST", info.URL+"/api/shutdown", nil)
		if e != nil {
			return e
		}
		req.Header.Set("Authorization", "Bearer "+string(key))
		client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, e := client.Do(req)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return errors.New("runner rejected shutdown")
		}
		fmt.Println("WebDeviceCable stopped.")
		return nil
	default:
		return errors.New("commands: start, open, stop, desktop, desktop-stop, serve, version")
	}
}
func defaultDataDirectory(root string) string {
	current := filepath.Join(root, "webdevicecable")
	if _, e := os.Lstat(current); !errors.Is(e, os.ErrNotExist) {
		return current
	}
	// Keep an existing installation's identity and grants in place. Renaming
	// the product must not create a second runner with a different identity.
	legacy := filepath.Join(root, "ai-bridge")
	if info, e := os.Lstat(legacy); e == nil && info.IsDir() {
		return legacy
	}
	return current
}
func readRuntime(dir string) (bridge.RuntimeInfo, error) {
	var info bridge.RuntimeInfo
	b, e := bridge.ReadLocalFile(filepath.Join(dir, "runner.json"), 1024)
	if e != nil {
		return info, e
	}
	e = json.Unmarshal(b, &info)
	if e == nil {
		e = bridge.ValidateRuntime(info)
	}
	return info, e
}
func alive(dir string, info bridge.RuntimeInfo) bool {
	if bridge.ValidateRuntime(info) != nil {
		return false
	}
	key, err := bridge.ReadLocalFile(filepath.Join(dir, "admin.key"), 64)
	if err != nil || len(key) != 64 {
		return false
	}
	var challenge [32]byte
	if _, err = rand.Read(challenge[:]); err != nil {
		return false
	}
	nonce := hex.EncodeToString(challenge[:])
	body, _ := json.Marshal(map[string]string{"nonce": nonce})
	req, _ := http.NewRequest("POST", info.URL+"/api/runtime", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	var result struct {
		PID   int    `json:"pid"`
		Proof string `json:"proof"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&result) != nil {
		return false
	}
	return result.PID == info.PID && hmac.Equal([]byte(result.Proof), []byte(bridge.RuntimeProof(string(key), nonce, info)))
}
func openPanel(dir string, info bridge.RuntimeInfo, printLink bool) error {
	if !alive(dir, info) {
		return errors.New("local runner could not be authenticated")
	}
	key, e := bridge.ReadLocalFile(filepath.Join(dir, "admin.key"), 64)
	if e != nil {
		return e
	}
	link := info.URL + "/#key=" + string(key)
	if printLink {
		fmt.Println(link)
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		return errors.New("could not open the browser; use WebDeviceCable open --print to copy the private control-panel link")
	}
	return nil
}
