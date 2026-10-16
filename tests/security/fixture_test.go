// Package security_test verifies native service boundaries in explicitly isolated fixtures.
package security_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fn2auth/internal/config"
	"fn2auth/internal/instance"
	"fn2auth/internal/service"
	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
	"github.com/user-no-found/FnPackKit/runtime/identity"
	"github.com/user-no-found/FnPackKit/runtime/peer"
)

type nativeFixture struct {
	cfg    config.Config
	pool   *instance.Pool
	client *http.Client
	root   string
	checks map[string]any
}

var users = []identity.User{
	{UID: 41001, GID: 41001, Name: "security_alice", Home: "/synthetic/alice"},
	{UID: 41002, GID: 41002, Name: "security_bob", Home: "/synthetic/bob"},
}

func structRequest(user identity.User) identity.Request {
	return identity.Request{UID: user.UID, Name: user.Name}
}

func openFixture(t *testing.T) *nativeFixture {
	t.Helper()
	root := os.Getenv("FN2AUTH_SECURITY_FIXTURE")
	if root == "" {
		t.Skip("set FN2AUTH_SECURITY_FIXTURE to an isolated native-fixture.py output")
	}
	if _, err := os.Stat(filepath.Join(root, "fixture.json")); err != nil {
		t.Fatal("isolated fixture marker required", err)
	}
	cfg, err := config.Load(filepath.Join(root, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	root, err = os.MkdirTemp(root, "security-")
	if err != nil {
		t.Fatal(err)
	}
	cfg.StateDir = filepath.Join(root, "state")
	cfg.RunDir = filepath.Join(root, "run")
	for _, dir := range []string{cfg.StateDir, cfg.RunDir, filepath.Join(cfg.StateDir, "users")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	pool := instance.New(cfg, "security-e2e", "Fn2Auth security fixture", func(request identity.Request) (identity.User, error) {
		for _, user := range users {
			if request.UID == user.UID && request.Name == user.Name {
				return user, nil
			}
		}
		return identity.User{}, errors.New("synthetic identity mismatch")
	})
	policy, err := gatewaypolicy.New(cfg.GatewayPolicy())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(cfg.RunDir, "security.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	trusted, err := peer.NewListener(listener, []uint32{uint32(os.Geteuid())}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	gateway := service.NewHandler(cfg, service.Metadata{Name: "Fn2Auth", Version: "test"}, policy, pool)
	server := &http.Server{Handler: gateway, ConnContext: trusted.ConnContext}
	go server.Serve(trusted)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", listener.Addr().String())
	}}
	fixture := &nativeFixture{cfg: cfg, pool: pool, root: root, client: &http.Client{Transport: transport, Timeout: 150 * time.Second}, checks: map[string]any{"identity_scope": "synthetic business UIDs; real OS peer and native PHP"}}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := pool.Close(); err != nil {
			t.Error(err)
		}
		gateway.Close()
		trusted.CloseConnections()
		listener.Close()
		transport.CloseIdleConnections()
		fixture.checks["passed"] = !t.Failed()
		data, _ := json.MarshalIndent(fixture.checks, "", "  ")
		if err := os.WriteFile(filepath.Join(root, "security-checks.json"), append(data, '\n'), 0600); err != nil {
			t.Error(err)
		}
		if evidence := os.Getenv("FN2AUTH_SECURITY_EVIDENCE"); evidence != "" {
			target := filepath.Join(evidence, t.Name()+".json")
			if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				t.Error(err)
			} else if err = os.WriteFile(target, append(data, '\n'), 0600); err != nil {
				t.Error(err)
			}
		}
	})
	t.Log("native security evidence:", root)
	return fixture
}

func (f *nativeFixture) request(t *testing.T, user identity.User, method, path, body string, headers map[string]string) (int, []byte, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, "http://nas.test"+f.cfg.Prefix+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Trim-Userid", strconv.FormatUint(uint64(user.UID), 10))
	req.Header.Set("X-Trim-Username", user.Name)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", "http://nas.test")
	req.Header.Set("X-Fn2Auth-Origin", "same-origin")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		if value == "" {
			req.Header.Del(key)
		} else {
			req.Header.Set(key, value)
		}
	}
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024))
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, data, response.Header
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("expected JSON response (%d bytes): %v", len(data), err)
	}
	return result
}
