package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/thomasmikava/WebDeviceCable/internal/bridge"
)

func launchConnectionAt(uri, dir string, port int) error {
	if _, err := bridge.ParseLaunchURI(uri); err != nil {
		return err
	}
	if err := run([]string{"start", "--no-open", "--data-dir", dir, "--port", fmt.Sprint(port)}); err != nil {
		return err
	}
	info, err := readRuntime(dir)
	if err != nil || !alive(dir, info) {
		return errors.New("the installed runner could not be authenticated")
	}
	key, err := bridge.ReadLocalFile(filepath.Join(dir, "admin.key"), 64)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"uri": uri})
	request, _ := http.NewRequest("POST", info.URL+"/api/launch-intents", bytes.NewReader(data))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(key))
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != 201 {
		return errors.New("this running WebDeviceCable version could not open the link; quit WDC, install the latest version, and try again from the website")
	}
	return openPanel(dir, info, false)
}
