// Command bridge exposes an isolated synthetic-user fixture to local browser E2E tests.
// It is never compiled into the application payload.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"fn2auth/internal/config"
	"fn2auth/internal/instance"
	"fn2auth/internal/service"
	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/peer"
)

func main() {
	fixture := flag.String("fixture", "", "native-fixture.py output directory")
	flag.Parse()
	if err := run(*fixture); err != nil {
		panic(err)
	}
}

func run(root string) error {
	if _, err := os.Stat(filepath.Join(root, "fixture.json")); err != nil {
		return errors.New("isolated fixture marker required")
	}
	cfg, err := config.Load(filepath.Join(root, "config.json"))
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(cfg.StateDir, "users"), 0700); err != nil {
		return err
	}
	user := identity.User{UID: 100001, GID: 100001, Name: "sun", Home: "/synthetic/fn2auth/sun", Shell: "/bin/sh", Groups: []uint32{100001}}
	pool := instance.New(cfg, "browser-e2e", "Fn2Auth", func(request identity.Request) (identity.User, error) {
		if request.UID != user.UID || request.Name != user.Name {
			return identity.User{}, errors.New("synthetic identity mismatch")
		}
		return user, nil
	})
	policy, err := gatewaypolicy.New(cfg.GatewayPolicy())
	if err != nil {
		return err
	}
	gateway := service.NewHandler(cfg, service.Metadata{Name: "Fn2Auth", Version: "fixture"}, policy, pool)
	defer gateway.Close()
	socket := filepath.Join(cfg.RunDir, "browser.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return err
	}
	defer os.Remove(socket)
	trusted, err := peer.NewListener(listener, []uint32{uint32(os.Geteuid())}, time.Hour)
	if err != nil {
		return err
	}
	defer trusted.CloseConnections()
	backend := &http.Server{Handler: gateway, ConnContext: trusted.ConnContext}
	go backend.Serve(trusted)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Transport: transport, Rewrite: func(request *httputil.ProxyRequest) {
		request.Out.URL.Scheme = "http"
		request.Out.URL.Host = "localhost"
		request.Out.Host = request.In.Host
		request.Out.Header.Set("X-Trim-Userid", strconv.FormatUint(uint64(user.UID), 10))
		request.Out.Header.Set("X-Trim-Username", user.Name)
	}}
	public, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}
	go server.Serve(public)
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"url": "http://" + public.Addr().String() + cfg.Prefix + "/", "fixture": root, "identity_scope": "synthetic business user over a real trusted Unix peer"})
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	<-ctx.Done()
	shutdown, stop := context.WithTimeout(context.Background(), time.Minute)
	defer stop()
	if err = server.Shutdown(shutdown); err != nil {
		return err
	}
	if err = backend.Shutdown(shutdown); err != nil {
		return err
	}
	return pool.Close()
}
