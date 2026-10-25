package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type RuntimeInfo struct {
	URL string `json:"url"`
	PID int    `json:"pid"`
}

func (a *App) Serve(ctx context.Context, listener net.Listener) error {
	go a.checkNative()
	stop := make(chan struct{})
	var once sync.Once
	server := &http.Server{Handler: a.Handler(listener.Addr().String(), func() { once.Do(func() { close(stop) }) }), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	info := RuntimeInfo{URL: "http://" + listener.Addr().String(), PID: os.Getpid()}
	b, _ := json.Marshal(info)
	path := filepath.Join(a.dir, "runner.json")
	if e := writeLocalFile(path, b); e != nil {
		listener.Close()
		return e
	}
	defer os.Remove(path)
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var runErr error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-stop:
			break loop
		case e := <-result:
			if !errors.Is(e, http.ErrServerClosed) {
				runErr = e
			}
			break loop
		case <-ticker.C:
			if e := a.tick(); e != nil {
				runErr = e
				break loop
			}
		}
	}
	deadline, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	server.Shutdown(deadline)
	server.Close()
	a.mu.Lock()
	e := a.update(func(s *State) error {
		a.closeConversations(s, "", "runner_stopped", a.now())
		for _, j := range s.Jobs {
			if !terminal(j.Status) {
				finish(j, "interrupted", "runner_stopped", "Runner stopped; this request will not be replayed.", a.now())
			}
		}
		return nil
	})
	a.mu.Unlock()
	if runErr != nil {
		return runErr
	}
	return e
}
