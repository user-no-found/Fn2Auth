// Package service owns the authenticated gateway and package-account lifecycle.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"fn2auth/internal/backup"
	"fn2auth/internal/config"
	"fn2auth/internal/instance"
	"fn2auth/internal/vault"
	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/peer"
)

// Metadata is injected from the application's one project.json at build time.
type Metadata struct{ Name, Version, Build string }

// Serve runs the public Unix gateway and private lifecycle control under a nonroot account.
func Serve(parent context.Context, cfg config.Config, metadata Metadata, ready ioReady) error {
	if os.Geteuid() == 0 {
		return errors.New("Fn2Auth service requires its nonroot package account")
	}
	syscall.Umask(0077)
	for _, directory := range []string{cfg.StateDir, cfg.RunDir, filepath.Join(cfg.StateDir, "users")} {
		if err := vault.Directory(directory); err != nil {
			return err
		}
	}
	lock, err := os.OpenFile(filepath.Join(cfg.RunDir, "service.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("service already running")
	}
	var trustedUIDs []uint32
	for _, name := range cfg.TrustedGatewayAccounts {
		account, err := identity.SystemAccount(name)
		if err != nil {
			return err
		}
		if account.UID == uint32(os.Geteuid()) {
			return errors.New("package account cannot be a trusted NAS gateway peer")
		}
		trustedUIDs = append(trustedUIDs, account.UID)
	}
	listener, err := unixListener(filepath.Join(cfg.AppDir, "app.sock"), 0600)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(filepath.Join(cfg.AppDir, "app.sock"))
	trusted, err := peer.NewListener(listener, trustedUIDs, time.Duration(cfg.ConnectionSeconds)*time.Second)
	if err != nil {
		return err
	}
	defer trusted.CloseConnections()
	policy, err := gatewaypolicy.New(cfg.GatewayPolicy())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	pool := instance.New(cfg, metadata.Build, metadata.Name, identity.Resolve)
	gateway := NewHandler(cfg, metadata, policy, pool)
	defer gateway.Close()
	server := &http.Server{Handler: gateway, ConnContext: trusted.ConnContext, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16384}
	control, err := controlServer(ctx, cfg, cancel)
	if err != nil {
		return err
	}
	defer control.Close()
	defer os.Remove(filepath.Join(cfg.RunDir, "control.sock"))
	result := make(chan error, 1)
	go func() { result <- server.Serve(trusted) }()
	if ready != nil {
		if _, err = ready.Write([]byte("ready\n")); err != nil {
			return err
		}
		ready.Close()
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case err = <-result:
			cancel()
		case <-ctx.Done():
			// Keep the control socket in stopping state until requests and workers finish.
			// A lifecycle caller may time out; that never turns an unfinished save into success.
			for {
				shutdown, stop := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownSeconds)*time.Second)
				shutdownErr := server.Shutdown(shutdown)
				stop()
				if shutdownErr == nil {
					if closeErr := pool.Close(); closeErr == nil {
						return nil
					}
				}
				time.Sleep(time.Second)
			}
		case <-ticker.C:
			go pool.RetireIdle()
		}
	}
}

type ioReady interface {
	Write([]byte) (int, error)
	Close() error
}

func unixListener(path string, mode os.FileMode) (*net.UnixListener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("socket path occupied by non-socket")
		}
		connection, dialErr := net.DialTimeout("unix", path, time.Second)
		if dialErr == nil {
			connection.Close()
			return nil, errors.New("socket already active")
		}
		if err = os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(path, mode); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// Gateway owns HTTP routing and the short-lived decrypted import previews.
type Gateway struct {
	http.Handler
	backups *backup.Manager
}

// Close erases outstanding import previews after requests finish.
func (gateway *Gateway) Close() { gateway.backups.Close() }

// NewHandler composes the shared trusted-transport policy with one application instance pool.
func NewHandler(cfg config.Config, metadata Metadata, policy *gatewaypolicy.Policy, pool *instance.Pool) *Gateway {
	backups := backup.New(cfg.MaxUsers)
	return &Gateway{backups: backups, Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		user, rejection := policy.Check(request)
		if rejection != nil {
			gatewaypolicy.WriteRejection(writer, request, rejection)
			return
		}
		suffix := strings.TrimPrefix(request.URL.Path, cfg.Prefix)
		if suffix == "/index.php" || strings.HasPrefix(suffix, "/index.php/") {
			failure(writer, "invalid_path", 403)
			return
		}
		if suffix == "" {
			http.Redirect(writer, request, cfg.Prefix+"/", http.StatusTemporaryRedirect)
			return
		}
		if suffix == "/_fn2auth/config" {
			if request.Method != "GET" {
				http.Error(writer, "method_not_allowed", 405)
				return
			}
			current, err := pool.Resolve(user)
			if err != nil {
				failure(writer, "identity_unavailable", 403)
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{"schema": 1, "display_name": metadata.Name, "version": metadata.Version, "gateway_prefix": cfg.Prefix, "user": map[string]string{"username": current.Name}})
			return
		}
		if strings.HasPrefix(suffix, "/_instance/") {
			failure(writer, "not_found", 404)
			return
		}
		if strings.HasPrefix(suffix, "/build/") || strings.HasPrefix(suffix, "/favicon") || suffix == "/manifest.json" || suffix == "/manifest.webmanifest" || suffix == "/logo.png" || suffix == "/source.tar.gz" {
			if request.Method != "GET" && request.Method != "HEAD" {
				failure(writer, "method_not_allowed", 405)
				return
			}
			http.StripPrefix(cfg.Prefix, http.FileServer(http.Dir(filepath.Join(cfg.AppDir, "upstream/app/public")))).ServeHTTP(writer, request)
			return
		}
		if strings.HasPrefix(suffix, "/api/") || strings.HasPrefix(suffix, "/user") || strings.HasPrefix(suffix, "/system/") || suffix == "/refresh-csrf" {
			if request.Header.Get("X-Fn2Auth-Origin") != "same-origin" {
				failure(writer, "same_origin_header_required", 403)
				return
			}
		}
		// No alternate login, user management, global settings or direct API-token surface.
		if strings.HasPrefix(suffix, "/api/v1/users") || strings.HasPrefix(suffix, "/api/v1/settings") && request.Method != "GET" || strings.HasPrefix(suffix, "/system/") || strings.HasPrefix(suffix, "/oauth/") || strings.HasPrefix(suffix, "/socialite/") || strings.HasPrefix(suffix, "/webauthn/") || suffix == "/user/login" || suffix == "/user" && request.Method != "GET" {
			failure(writer, "route_unavailable", 403)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, cfg.RequestBodyMB*1024*1024)
		worker, release, err := pool.Acquire(request.Context(), user)
		if err != nil {
			failure(writer, "vault_unavailable", 503)
			return
		}
		defer release()
		if strings.HasPrefix(suffix, "/_fn2auth/backup/") {
			backups.Serve(writer, request, strings.TrimPrefix(suffix, "/_fn2auth/backup/"), user.UID, worker.Backup)
			return
		}
		if strings.HasPrefix(suffix, "/storage/icons/") {
			http.StripPrefix(cfg.Prefix+"/storage/", http.FileServer(http.Dir(filepath.Join(worker.State.Directory, "storage/app/public")))).ServeHTTP(writer, request)
			return
		}
		proxy(cfg, worker).ServeHTTP(writer, request)
	})}
}

func failure(writer http.ResponseWriter, code string, status int) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"schema": 1, "status": "rejected", "code": code})
}
