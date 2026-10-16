// Package instance runs isolated PHP state under the dedicated application account.
package instance

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fn2auth/internal/config"
	"fn2auth/internal/vault"
	"github.com/user-no-found/FnPackKit/runtime/process"
)

// Worker is a private PHP endpoint; its credentials never cross into a browser.
type Worker struct {
	Child          *process.Child
	Socket, Secret string
	Transport      *http.Transport
	State          vault.State
	php, app       string
	environment    []string
}

func randomToken() (string, error) {
	var data [32]byte
	_, err := rand.Read(data[:])
	return base64.RawURLEncoding.EncodeToString(data[:]), err
}

// Start migrates the selected vault and waits for authenticated private readiness.
func Start(ctx context.Context, cfg config.Config, state vault.State, version, displayName string) (*Worker, error) {
	directory := filepath.Join(cfg.RunDir, strconv.FormatUint(uint64(state.User.UID), 10))
	if err := vault.Directory(directory); err != nil {
		return nil, err
	}
	socket := filepath.Join(directory, "php.sock")
	if len(socket) > 100 {
		return nil, errors.New("private socket path exceeds Unix socket limit")
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("unsafe private socket entry")
		}
		if err = os.Remove(socket); err != nil {
			return nil, err
		}
	}
	secret, err := randomToken()
	if err != nil {
		return nil, err
	}
	readyID, err := randomToken()
	if err != nil {
		return nil, err
	}
	environment := Environment(cfg, state, directory, secret, readyID, displayName)
	php := filepath.Join(cfg.AppDir, "runtime/frankenphp")
	app := filepath.Join(cfg.AppDir, "upstream/app")
	if err = initialize(ctx, cfg, state, php, app, environment, version); err != nil {
		return nil, err
	}
	ini := fmt.Sprintf("memory_limit=%dM\nupload_max_filesize=%dM\npost_max_size=%dM\nmax_execution_time=%d\n", cfg.PHPMemoryMB, cfg.RequestBodyMB, cfg.RequestBodyMB, cfg.ShutdownSeconds)
	if err = os.WriteFile(filepath.Join(directory, "php.ini"), []byte(ini), 0600); err != nil {
		return nil, err
	}
	configuration := map[string]any{"admin": map[string]any{"disabled": true}, "apps": map[string]any{
		"frankenphp": map[string]any{"num_threads": cfg.PHPThreads, "max_threads": cfg.PHPThreads},
		"http": map[string]any{"servers": map[string]any{"private": map[string]any{
			"listen": []string{"unix/" + socket}, "routes": []any{map[string]any{"handle": []any{
				map[string]any{"handler": "rewrite", "uri": "/index.php"},
				map[string]any{"handler": "php", "root": filepath.Join(app, "public"), "split_path": []string{".php"}},
			}}},
		}}},
	}}
	data, _ := json.Marshal(configuration)
	configFile := filepath.Join(directory, "server.json")
	if err = os.WriteFile(configFile, data, 0600); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(state.Directory, "storage/logs/php.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	command := exec.Command(php, "run", "--config", configFile)
	command.Dir = app
	command.Env = environment
	command.Stdout = log
	command.Stderr = log
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	child, err := process.Start(command)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, ResponseHeaderTimeout: cfg.StartupTimeout(), MaxIdleConnsPerHost: cfg.PHPThreads}
	worker := &Worker{Child: child, Socket: socket, Secret: secret, Transport: transport, State: state, php: php, app: app, environment: environment}
	client := &http.Client{Transport: transport, Timeout: time.Second}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost"+cfg.Prefix+"/_instance/ready", nil)
		request.Header.Set("X-Fn2auth-Instance", secret)
		if response, requestErr := client.Do(request); requestErr == nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 100))
			response.Body.Close()
			if response.StatusCode == 200 && string(body) == readyID {
				return worker, nil
			}
		}
		select {
		case <-ctx.Done():
			_ = child.Terminate(time.Second)
			return worker, errors.New("PHP readiness timed out; worker retained until reaped")
		case <-child.Done():
			return worker, errors.New("PHP exited before readiness")
		case <-ticker.C:
		}
	}
}

// Environment provides private paths and strips platform/API credentials from PHP.
func Environment(cfg config.Config, state vault.State, run, secret, ready, displayName string) []string {
	values := map[string]string{
		"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8", "HOME": state.Directory, "TMPDIR": filepath.Join(state.Directory, "tmp"),
		"XDG_CONFIG_HOME": filepath.Join(state.Directory, "cache"), "XDG_DATA_HOME": filepath.Join(state.Directory, "cache"),
		"APP_ENV": "production", "APP_DEBUG": "false", "APP_KEY": state.Key, "APP_NAME": displayName,
		"APP_URL": cfg.Prefix, "ASSET_URL": cfg.Prefix, "APP_SUBDIRECTORY": strings.TrimPrefix(cfg.Prefix, "/"),
		"APP_LOCALE": cfg.Locale, "APP_FALLBACK_LOCALE": "en", "AUTHENTICATION_GUARD": "reverse-proxy-guard",
		"AUTH_PROXY_HEADER_FOR_USER": "HTTP_REMOTE_USER", "TRUSTED_PROXIES": "127.0.0.1",
		"DB_CONNECTION": "sqlite", "DB_DATABASE": filepath.Join(state.Directory, "database.sqlite"), "CACHE_STORE": "file", "CACHE_DRIVER": "file",
		"SESSION_DRIVER": "file", "SESSION_COOKIE": "fn2auth_" + strconv.FormatUint(uint64(state.User.UID), 10) + "_session", "SESSION_PATH": cfg.Prefix,
		"LOG_CHANNEL": "single", "QUEUE_CONNECTION": "sync", "MAIL_MAILER": "log", "BROADCAST_CONNECTION": "log",
		"APP_CONFIG_CACHE": filepath.Join(state.Directory, "cache/config.php"), "APP_ROUTES_CACHE": filepath.Join(state.Directory, "cache/routes.php"),
		"APP_SERVICES_CACHE": filepath.Join(state.Directory, "cache/services.php"), "APP_PACKAGES_CACHE": filepath.Join(state.Directory, "cache/packages.php"),
		"VIEW_COMPILED_PATH": filepath.Join(state.Directory, "storage/framework/views"), "PHP_INI_SCAN_DIR": run,
		"FN2AUTH_STATE": state.Directory, "FN2AUTH_PREFIX": cfg.Prefix, "FN2AUTH_USERNAME": state.User.Name,
		"FN2AUTH_INSTANCE_SECRET": secret, "FN2AUTH_READY_ID": ready,
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}
