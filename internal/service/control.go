package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"fn2auth/internal/config"
)

func controlServer(ctx context.Context, cfg config.Config, stop context.CancelFunc) (*http.Server, error) {
	listener, err := unixListener(filepath.Join(cfg.RunDir, "control.sock"), 0600)
	if err != nil {
		return nil, err
	}
	var stopping atomic.Bool
	server := &http.Server{ReadHeaderTimeout: time.Second, MaxHeaderBytes: 1024, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/status" && r.Method == "GET" {
			if stopping.Load() || ctx.Err() != nil {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(200)
			}
			return
		}
		if r.URL.Path == "/stop" && r.Method == "POST" {
			stopping.Store(true)
			w.WriteHeader(202)
			stop()
			return
		}
		http.NotFound(w, r)
	})}
	go server.Serve(listener)
	return server, nil
}

func controlRequest(ctx context.Context, cfg config.Config, method, path string) (int, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(cfg.RunDir, "control.sock"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	request, _ := http.NewRequestWithContext(ctx, method, "http://localhost"+path, nil)
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

// Status returns the framework's 0 running, 3 stopped, or 1 error status.
func Status(cfg config.Config) int {
	code, err := controlRequest(context.Background(), cfg, "GET", "/status")
	if err == nil && code == 200 {
		return 0
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return 3
	}
	return 1
}

// Start detaches the same executable and waits for its authenticated local readiness pipe.
func Start(cfg config.Config, configFile string) error {
	if Status(cfg) == 0 {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	defer writer.Close()
	log, err := os.OpenFile(filepath.Join(cfg.StateDir, "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	command := exec.Command(self, "serve", "--config", configFile, "--ready-fd", "3")
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	command.Stdout = log
	command.Stderr = log
	command.ExtraFiles = []*os.File{writer}
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = command.Start(); err != nil {
		return err
	}
	writer.Close()
	if err = reader.SetReadDeadline(time.Now().Add(cfg.StartupTimeout())); err != nil {
		_ = command.Process.Signal(syscall.SIGTERM)
		return err
	}
	text, err := bufio.NewReader(io.LimitReader(reader, 64)).ReadString('\n')
	if err != nil || text != "ready\n" {
		_ = command.Process.Signal(syscall.SIGTERM)
		_ = command.Wait()
		return errors.New("service failed to become ready")
	}
	return command.Process.Release()
}

// Stop waits until the control listener has closed after graceful shutdown.
func Stop(cfg config.Config) error {
	if Status(cfg) == 3 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownSeconds+5)*time.Second)
	defer cancel()
	code, err := controlRequest(ctx, cfg, "POST", "/stop")
	if err != nil || code != 202 {
		return errors.New("service stop request failed")
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if Status(cfg) == 3 {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("service shutdown not complete")
		case <-ticker.C:
		}
	}
}
