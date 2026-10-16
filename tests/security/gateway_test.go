package security_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/peer"
)

func TestNativeBoundaries(t *testing.T) {
	f := openFixture(t)
	// This diagnostic exists only in the explicitly marked copied fixture. The real
	// private-channel authentication runs before it, with the real PHP CGI mapping.
	bootstrap := filepath.Join(f.cfg.AppDir, "upstream/app/bootstrap/fn2auth-request.php")
	original, err := os.ReadFile(bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	probe := `if (parse_url($_SERVER['REQUEST_URI'] ?? '', PHP_URL_PATH) === getenv('FN2AUTH_PREFIX').'/_security/probe') {
    header('Content-Type: application/json');
    echo json_encode(['username'=>$_SERVER['HTTP_REMOTE_USER'],
        'instance_authenticated'=>hash_equals(getenv('FN2AUTH_INSTANCE_SECRET'),$_SERVER['HTTP_X_FN2AUTH_INSTANCE']??''),
        'forwarded_host'=>$_SERVER['HTTP_X_FORWARDED_HOST']??null,
        'forwarded_for'=>$_SERVER['HTTP_X_FORWARDED_FOR']??null,
        'authorization'=>$_SERVER['HTTP_AUTHORIZATION']??null,
        'cookie'=>$_SERVER['HTTP_COOKIE']??'']); exit;
}
`
	if err = os.WriteFile(bootstrap, append(original, []byte("\n"+probe)...), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.WriteFile(bootstrap, original, 0600); err != nil {
			t.Error(err)
		}
	})
	t.Run("untrusted kernel peer", func(t *testing.T) {
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(f.cfg.RunDir, "untrusted.sock"), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		trusted, err := peer.NewListener(listener, []uint32{uint32(os.Geteuid() + 1)}, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("untrusted peer reached handler") }), ConnContext: trusted.ConnContext}
		go server.Serve(trusted)
		defer server.Close()
		connection, err := net.Dial("unix", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		connection.SetDeadline(time.Now().Add(2 * time.Second))
		io.WriteString(connection, "GET / HTTP/1.1\r\nHost: nas.test\r\nX-Trim-Userid: 41001\r\nX-Trim-Username: security_alice\r\n\r\n")
		data, _ := io.ReadAll(connection)
		if len(data) > 0 {
			t.Fatal("untrusted peer received an HTTP response")
		}
		f.checks["untrusted_kernel_peer_rejected"] = true
	})
	t.Run("canonical paths and origin", func(t *testing.T) {
		for _, path := range []string{"/api/v1/user", "/index.php/api/v1/user", "/index.php/api/v1/twofaccounts/1/otp"} {
			code, _, _ := f.request(t, users[0], "GET", path, "", map[string]string{"X-Fn2Auth-Origin": "", "Origin": "", "Sec-Fetch-Site": ""})
			if code != 403 {
				t.Fatalf("%s returned %d", path, code)
			}
		}
		code, _, _ := f.request(t, users[0], "GET", "/_instance/ready", "", nil)
		if code != 404 {
			t.Fatalf("private ready returned %d", code)
		}
		code, _, _ = f.request(t, users[0], "POST", "/api/v1/twofaccounts", "{}", map[string]string{"Origin": "http://evil.test", "Sec-Fetch-Site": "cross-site"})
		if code != 403 {
			t.Fatalf("cross-site write returned %d", code)
		}
		f.checks["canonical_paths_and_origin_rejected"] = true
	})
	t.Run("real PHP header normalization", func(t *testing.T) {
		code, body, _ := f.request(t, users[0], "GET", "/_security/probe", "", map[string]string{
			"X_Forwarded_Host": "evil.test", "X_Forwarded_For": "198.51.100.7", "X_Fn2auth_Instance": "attacker",
			"Remote_User": "security_bob", "X_Trim_Username": "security_bob", "Authorization": "Bearer attacker"})
		if code != 200 {
			t.Fatalf("PHP probe returned %d; inspect isolated fixture logs", code)
		}
		data := decode(t, body)
		if data["username"] != users[0].Name || data["instance_authenticated"] != true || data["forwarded_host"] != nil || data["forwarded_for"] != nil || data["authorization"] != nil {
			t.Fatalf("private PHP boundary failed: %v", data)
		}
		f.checks["php_header_aliases_filtered"] = true
	})
	t.Run("two business UID namespaces", func(t *testing.T) {
		var keys []string
		var aliceCookie string
		for index, user := range users {
			code, body, headers := f.request(t, user, "GET", "/api/v1/user", "", nil)
			if code != 200 && code != 201 {
				t.Fatalf("user %d API returned %d; inspect isolated fixture logs", user.UID, code)
			}
			if !strings.Contains(string(body), user.Name) {
				t.Fatalf("user %d API omitted its own identity", user.UID)
			}
			if strings.Contains(string(body), users[1-index].Name) {
				t.Fatal("cross-user API identity")
			}
			_, _, headers = f.request(t, user, "GET", "/refresh-csrf", "", nil)
			for _, cookie := range (&http.Response{Header: headers}).Cookies() {
				if cookie.Path != f.cfg.Prefix+"/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode {
					t.Fatal("unsafe scoped session cookie")
				}
				if index == 0 && strings.HasPrefix(cookie.Name, "fn2auth_") {
					aliceCookie = cookie.Name + "=" + cookie.Value
				}
			}
			state := filepath.Join(f.cfg.StateDir, "users", strconv.FormatUint(uint64(user.UID), 10))
			key, err := os.ReadFile(filepath.Join(state, "app.key"))
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(key)
			keys = append(keys, hex.EncodeToString(digest[:]))
			if err = os.WriteFile(filepath.Join(state, "storage/app/public/icons/namespace.txt"), []byte(user.Name), 0600); err != nil {
				t.Fatal(err)
			}
			code, body, _ = f.request(t, user, "GET", "/storage/icons/namespace.txt", "", nil)
			if code != 200 || string(body) != user.Name {
				t.Fatalf("UID %d storage routing failed", user.UID)
			}
		}
		if keys[0] == keys[1] {
			t.Fatal("business users share APP_KEY")
		}
		if aliceCookie == "" {
			t.Fatal("no user-specific session cookie observed")
		}
		code, body, _ := f.request(t, users[1], "GET", "/_security/probe", "", map[string]string{"Cookie": aliceCookie})
		if code != 200 {
			t.Fatal("second user probe failed")
		}
		data := decode(t, body)
		if data["username"] != users[1].Name || strings.Contains(data["cookie"].(string), "fn2auth_41001_session") {
			t.Fatal("cross-user cookie forwarded")
		}
		f.checks["two_synthetic_users_real_php_api_storage_session_isolation"] = true
		f.checks["distinct_key_sha256"] = keys
	})
	t.Run("unfinished migration is not retried", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		worker, release, err := f.pool.Acquire(ctx, structRequest(users[0]))
		if err != nil {
			t.Fatal(err)
		}
		release()
		if err = worker.Child.Terminate(3 * time.Second); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(worker.State.Directory, "migration-in-progress.json")
		if err = os.WriteFile(marker, []byte(`{"status":"in-progress"}`), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err = f.pool.Acquire(ctx, structRequest(users[0]))
		if err == nil || !strings.Contains(err.Error(), "interrupted") {
			t.Fatalf("interrupted migration not rejected: %v", err)
		}
		f.checks["interrupted_migration_rejected"] = true
	})
}
