package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/thomasmikava/WebDeviceCable/internal/relay"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	dir := flag.String("data-dir", "/data", "private persistent relay directory")
	listen := flag.String("listen", ":8080", "HTTP listen address (put HTTPS termination in front)")
	flag.Parse()
	config, e := configFromEnvironment(*dir)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	service, e := relay.Open(config)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	defer service.Close()
	server := &http.Server{Addr: *listen, Handler: service.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		end, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		server.Shutdown(end)
	}()
	fmt.Println(service.String())
	if e = server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}

func configFromEnvironment(directory string) (relay.Config, error) {
	for _, name := range []string{"WDC_PROJECT_KEY", "WDC_PROJECT_KEY_FILE", "WDC_APP_ID", "WDC_APP_NAME", "WDC_APP_ORIGIN"} {
		if os.Getenv(name) != "" {
			return relay.Config{}, fmt.Errorf("%s is no longer a relay setting; use WDC_ADMIN_KEY_FILE and register projects through the management API", name)
		}
	}
	key, err := secret("WDC_ADMIN_KEY")
	return relay.Config{Directory: directory, AdminKey: key}, err
}

func secret(name string) (string, error) {
	if file := os.Getenv(name + "_FILE"); file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("cannot read %s secret file", name)
		}
		value := strings.TrimSpace(string(b))
		if value == "" {
			return "", fmt.Errorf("%s secret file is empty", name)
		}
		return value, nil
	}
	return os.Getenv(name), nil
}
