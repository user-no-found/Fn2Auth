package security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fn2auth/internal/config"
	"fn2auth/internal/instance"
	"fn2auth/internal/service"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

func TestNativeLifecycle(t *testing.T) {
	if path := os.Getenv("FN2AUTH_MIGRATION_HELPER"); path != "" {
		cfg, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		pool := instance.New(cfg, "migration-fault", "Fn2Auth fixture",
			func(request identity.Request) (identity.User, error) { return users[0], nil })
		_, _, err = pool.Acquire(context.Background(), structRequest(users[0]))
		t.Fatalf("blocking migration returned unexpectedly: %v", err)
	}
	t.Run("inflight pool close", func(t *testing.T) {
		f := openFixture(t)
		_, release, err := f.pool.Acquire(context.Background(), structRequest(users[0]))
		if err != nil {
			t.Fatal(err)
		}
		if err = f.pool.Close(); err == nil {
			release()
			t.Fatal("busy pool falsely reported complete shutdown")
		}
		release()
		if err = f.pool.Close(); err != nil {
			t.Fatal(err)
		}
		f.checks["busy_pool_shutdown_does_not_report_success"] = true
	})
	t.Run("migration parent crash", func(t *testing.T) {
		f := openFixture(t)
		artisan := filepath.Join(f.cfg.AppDir, "upstream/app/artisan")
		original, err := os.ReadFile(artisan)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(artisan)
		if err != nil {
			t.Fatal(err)
		}
		hook := `<?php
if (($argv[1] ?? '') === 'migrate') {
    file_put_contents(getenv('FN2AUTH_STATE').'/migration-cli.pid', (string)getmypid());
    while (true) { usleep(100000); }
}
`
		modified := strings.Replace(string(original), "<?php", hook, 1)
		if modified == string(original) {
			t.Fatal("fixture artisan PHP entry not found")
		}
		if err = os.WriteFile(artisan, []byte(modified), info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.WriteFile(artisan, original, info.Mode().Perm()); err != nil {
				t.Error(err)
			}
		})
		cfgFile := filepath.Join(f.root, "migration-helper.json")
		data, err := json.Marshal(f.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(cfgFile, data, 0600); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(os.Args[0], "-test.run=^TestNativeLifecycle$", "-test.timeout=45s")
		command.Env = append(os.Environ(), "FN2AUTH_MIGRATION_HELPER="+cfgFile)
		var diagnostic bytes.Buffer
		command.Stdout = &diagnostic
		command.Stderr = &diagnostic
		if err = command.Start(); err != nil {
			t.Fatal(err)
		}
		finished := make(chan struct{})
		var waitErr error
		go func() {
			waitErr = command.Wait()
			close(finished)
		}()
		t.Cleanup(func() {
			select {
			case <-finished:
			default:
				_ = command.Process.Kill()
				<-finished
			}
		})
		state := filepath.Join(f.cfg.StateDir, "users", strconv.FormatUint(uint64(users[0].UID), 10))
		pidFile := filepath.Join(state, "migration-cli.pid")
		pid := 0
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-finished:
				t.Fatalf("native migration helper exited before fault injection: %v\n%s", waitErr, diagnostic.String())
			default:
			}
			if data, err := os.ReadFile(pidFile); err == nil {
				pid, _ = strconv.Atoi(string(data))
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if pid < 1 {
			_ = command.Process.Kill()
			<-finished
			t.Fatalf("native migration fault injection never became active: %v\n%s", waitErr, diagnostic.String())
		}
		_, started, err := processIdentity(pid)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if state, same, err := processIdentity(pid); err == nil && same == started && state != "Z" {
				process, err := os.FindProcess(pid)
				if err == nil {
					_ = process.Kill()
				}
			}
		})
		if _, err = os.Stat(filepath.Join(state, "migration-in-progress.json")); err != nil {
			t.Fatal("migration has no durable in-progress marker")
		}
		if err = command.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		<-finished
		deadline = time.Now().Add(5 * time.Second)
		stopped := false
		for time.Now().Before(deadline) {
			status, identity, err := processIdentity(pid)
			if errors.Is(err, os.ErrNotExist) || err == nil && (identity != started || status == "Z") {
				stopped = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !stopped {
			t.Fatal("migration CLI survived its parent crash")
		}
		_, _, err = f.pool.Acquire(context.Background(), structRequest(users[0]))
		if err == nil || !strings.Contains(err.Error(), "interrupted") {
			t.Fatalf("crashed migration was automatically retried: %v", err)
		}
		f.checks["parent_crash_reaps_native_migration_and_blocks_retry"] = true
	})
	t.Run("stop timeout remains a failure", func(t *testing.T) {
		f := openFixture(t)
		cfg := f.cfg
		cfg.ShutdownSeconds = 5
		listener, err := net.Listen("unix", filepath.Join(cfg.RunDir, "control.sock"))
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/stop" {
				w.WriteHeader(202)
			} else {
				w.WriteHeader(503)
			}
		})}
		go server.Serve(listener)
		defer server.Close()
		if err = service.Stop(cfg); err == nil {
			t.Fatal("unfinished stop incorrectly reported success")
		}
		if service.Status(cfg) != 1 {
			t.Fatal("stopping listener incorrectly reported stopped")
		}
		server.Close()
		if err = service.Stop(cfg); err != nil {
			t.Fatal("completed stop was not idempotent", err)
		}
		f.checks["real_unix_control_timeout_reports_failure"] = true
	})
}

// processIdentity permits cleanup only of the exact child observed in this fixture.
func processIdentity(pid int) (state, started string, err error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", "", err
	}
	end := strings.LastIndexByte(string(data), ')')
	fields := strings.Fields(string(data[end+1:]))
	if end < 0 || len(fields) < 20 {
		return "", "", errors.New("invalid child process identity")
	}
	return fields[0], fields[19], nil
}
