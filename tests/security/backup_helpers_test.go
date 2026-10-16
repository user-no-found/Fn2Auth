package security_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/user-no-found/FnPackKit/runtime/identity"
)

const passphrase = "synthetic backup password 2026"

func encodeJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (f *nativeFixture) session(t *testing.T, user identity.User) string {
	t.Helper()
	code, _, _ := f.request(t, user, "GET", "/api/v1/user", "", nil)
	if code != 200 && code != 201 {
		t.Fatalf("user bootstrap status %d", code)
	}
	code, _, headers := f.request(t, user, "GET", "/refresh-csrf", "", nil)
	if code != 200 {
		t.Fatalf("session status %d", code)
	}
	wanted := "fn2auth_" + strconv.FormatUint(uint64(user.UID), 10) + "_session"
	for _, cookie := range (&http.Response{Header: headers}).Cookies() {
		if cookie.Name == wanted {
			return cookie.Name + "=" + cookie.Value
		}
	}
	t.Fatal("missing private session cookie")
	return ""
}

func (f *nativeFixture) backup(t *testing.T, user identity.User, cookie, action string, body any) (int, []byte) {
	t.Helper()
	status, data, _ := f.request(t, user, "POST", "/_fn2auth/backup/"+action, encodeJSON(t, body), map[string]string{"Cookie": cookie})
	return status, data
}

func (f *nativeFixture) export(t *testing.T, user identity.User, cookie string) []byte {
	t.Helper()
	status, data := f.backup(t, user, cookie, "export", map[string]any{"passphrase": passphrase})
	if status != 200 {
		t.Fatalf("export status %d (%s)", status, publicCode(data))
	}
	return data
}

func publicCode(data []byte) string {
	var body struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(data, &body)
	return body.Code
}

func encryptDocument(t *testing.T, document map[string]any) []byte {
	t.Helper()
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		t.Fatal(err)
	}
	recipient.SetWorkFactor(16)
	var out bytes.Buffer
	writer, err := age.Encrypt(&out, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte(encodeJSON(t, document))); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func decryptDocument(t *testing.T, data []byte) map[string]any {
	t.Helper()
	key, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		t.Fatal(err)
	}
	key.SetMaxWorkFactor(16)
	reader, err := age.Decrypt(bytes.NewReader(data), key)
	if err != nil {
		t.Fatal("export is not decryptable age ciphertext:", err)
	}
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, plain)
}

func (f *nativeFixture) preview(t *testing.T, user identity.User, cookie string, ciphertext []byte) string {
	t.Helper()
	status, data := f.backup(t, user, cookie, "preview", map[string]any{"passphrase": passphrase, "encrypted": base64.StdEncoding.EncodeToString(ciphertext)})
	if status != 200 {
		t.Fatalf("preview status %d (%s)", status, publicCode(data))
	}
	if bytes.Contains(data, []byte(`"secret"`)) {
		t.Fatal("preview exposed OTP secrets")
	}
	summary := decode(t, data)
	token, ok := summary["preview_token"].(string)
	if !ok || len(token) != 43 {
		t.Fatal("invalid preview token")
	}
	return token
}

func (f *nativeFixture) confirm(t *testing.T, user identity.User, cookie, token string, want int) map[string]any {
	t.Helper()
	status, data := f.backup(t, user, cookie, "import", map[string]any{"preview_token": token, "confirm": true})
	if status != want {
		t.Fatalf("confirm status %d, want %d (%s)", status, want, publicCode(data))
	}
	return decode(t, data)
}

func accounts(t *testing.T, document map[string]any) []any {
	t.Helper()
	rows, ok := document["accounts"].([]any)
	if !ok {
		t.Fatal("backup lacks accounts")
	}
	return rows
}

func counter(t *testing.T, document map[string]any) int {
	t.Helper()
	for _, item := range accounts(t, document) {
		row := item.(map[string]any)
		if row["otp_type"] == "hotp" {
			return int(row["counter"].(float64))
		}
	}
	t.Fatal("HOTP record absent")
	return -1
}

func (f *nativeFixture) sql(t *testing.T, user identity.User, statement string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	path := filepath.Join(f.cfg.StateDir, "users", strconv.FormatUint(uint64(user.UID), 10), "database.sqlite")
	script := filepath.Join(f.root, "fixture-sql.php")
	if err := os.WriteFile(script, []byte(`<?php $db=new PDO('sqlite:'.$argv[1]);$db->exec(stream_get_contents(STDIN));`), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, filepath.Join(f.cfg.AppDir, "runtime/frankenphp"), "php-cli", script, path)
	command.Env = []string{"PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewBufferString(statement)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixture SQL failed (%d diagnostic bytes): %v", len(output), err)
	}
}

func saveCiphertext(t *testing.T, f *nativeFixture, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.root, "synthetic-otp.age"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
