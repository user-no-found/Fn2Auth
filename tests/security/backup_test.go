package security_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeBackup(t *testing.T) {
	f := openFixture(t)
	alice, bob := f.session(t, users[0]), f.session(t, users[1])
	// Synthetic OTP backup checks must not depend on remote logo providers.
	for index, cookie := range []string{alice, bob} {
		code, _, _ := f.request(t, users[index], "PUT", "/api/v1/user/preferences/getOfficialIcons", `{"value":false}`, map[string]string{"Cookie": cookie})
		if code != 201 {
			t.Fatalf("synthetic fixture preference status %d", code)
		}
	}
	f.checks["synthetic_fixture_external_icon_fetch_disabled"] = true
	var hotpID int
	for _, kind := range []string{"totp", "hotp"} {
		row := map[string]any{"otp_type": kind, "service": "Synthetic " + kind, "account": "fixture@example.invalid", "secret": "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "algorithm": "sha1", "digits": 6}
		if kind == "hotp" {
			row["counter"] = 10
		} else {
			row["period"] = 30
		}
		code, data, _ := f.request(t, users[0], "POST", "/api/v1/twofaccounts", encodeJSON(t, row), map[string]string{"Cookie": alice})
		if code != 201 {
			t.Fatalf("create synthetic %s status %d", kind, code)
		}
		if kind == "hotp" {
			hotpID = int(decode(t, data)["id"].(float64))
		}
	}
	ciphertext := f.export(t, users[0], alice)
	saveCiphertext(t, f, ciphertext)
	initial := decryptDocument(t, ciphertext)
	if len(accounts(t, initial)) != 2 || counter(t, initial) != 10 {
		t.Fatal("initial encrypted export differs from fixture")
	}
	f.checks["age_encrypted_export_decrypt"] = true
	t.Run("wrong passphrase and required session", func(t *testing.T) {
		code, data := f.backup(t, users[0], alice, "preview", map[string]any{"passphrase": "incorrect but long password", "encrypted": base64.StdEncoding.EncodeToString(ciphertext)})
		if code != 400 || publicCode(data) != "decryption_failed" {
			t.Fatalf("wrong passphrase status %d", code)
		}
		code, _ = f.backup(t, users[0], "", "export", map[string]any{"passphrase": passphrase})
		if code != 403 {
			t.Fatal("missing session accepted")
		}
		f.checks["wrong_passphrase_and_missing_session_rejected"] = true
	})
	t.Run("UID session and one-shot binding", func(t *testing.T) {
		token := f.preview(t, users[0], alice, ciphertext)
		f.confirm(t, users[1], bob, token, 409)
		otherSession := f.session(t, users[0])
		if otherSession == alice {
			t.Fatal("fixture did not establish different page session")
		}
		f.confirm(t, users[0], otherSession, token, 409)
		result := f.confirm(t, users[0], alice, token, 200)
		if result["skipped"] != float64(2) {
			t.Fatal("same-user duplicate import not skipped")
		}
		f.confirm(t, users[0], alice, token, 409)
		f.checks["preview_uid_session_once"] = true
	})
	t.Run("portable import and duplicate retention", func(t *testing.T) {
		token := f.preview(t, users[1], bob, ciphertext)
		result := f.confirm(t, users[1], bob, token, 200)
		if result["imported"] != float64(2) {
			t.Fatal("portable import did not create both accounts")
		}
		result = f.confirm(t, users[1], bob, f.preview(t, users[1], bob, ciphertext), 200)
		if result["skipped"] != float64(2) || len(accounts(t, decryptDocument(t, f.export(t, users[1], bob)))) != 2 {
			t.Fatal("duplicate import changed account cardinality")
		}
		f.checks["portable_import_and_dedup"] = true
	})
	t.Run("transaction rollback", func(t *testing.T) {
		rows := accounts(t, decryptDocument(t, ciphertext))
		rows[0].(map[string]any)["service"] = "rollback_first"
		rows[1].(map[string]any)["service"] = "rollback_reject"
		altered := encryptDocument(t, map[string]any{"schema": "fn2auth-otp-v1", "accounts": rows})
		f.sql(t, users[0], `CREATE TRIGGER security_abort BEFORE INSERT ON twofaccounts WHEN NEW.otp_type='hotp' BEGIN SELECT RAISE(ABORT,'security_fixture_rejection'); END;`)
		token := f.preview(t, users[0], alice, altered)
		f.confirm(t, users[0], alice, token, 503)
		f.confirm(t, users[0], alice, token, 409)
		if len(accounts(t, decryptDocument(t, f.export(t, users[0], alice)))) != 2 {
			t.Fatal("failed import partially committed")
		}
		f.sql(t, users[0], "DROP TRIGGER security_abort;")
		f.checks["php_transaction_rollback_and_failed_token_consumption"] = true
	})
	t.Run("concurrent HOTP and old restore", func(t *testing.T) {
		token := f.preview(t, users[0], alice, ciphertext)
		const operations = 6
		var wait sync.WaitGroup
		results := make([]int, operations+1)
		for index := 0; index < operations; index++ {
			wait.Add(1)
			go func(index int) {
				defer wait.Done()
				code, _, _ := f.request(t, users[0], "GET", fmt.Sprintf("/api/v1/twofaccounts/%d/otp", hotpID), "", map[string]string{"Cookie": alice})
				results[index] = code
			}(index)
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			code, _ := f.backup(t, users[0], alice, "import", map[string]any{"preview_token": token, "confirm": true})
			results[operations] = code
		}()
		wait.Wait()
		for _, code := range results {
			if code != 200 {
				t.Fatalf("concurrent operation status %d", code)
			}
		}
		now := decryptDocument(t, f.export(t, users[0], alice))
		if counter(t, now) != 10+operations {
			t.Fatalf("HOTP counter = %d, want %d", counter(t, now), 10+operations)
		}
		f.confirm(t, users[0], alice, f.preview(t, users[0], alice, ciphertext), 200)
		if counter(t, decryptDocument(t, f.export(t, users[0], alice))) != 10+operations {
			t.Fatal("old restore rolled HOTP counter back")
		}
		f.checks["concurrent_hotp_restore_no_lost_increment"] = true
	})
	t.Run("real expiry", func(t *testing.T) {
		if os.Getenv("FN2AUTH_SECURITY_EXPIRY") != "1" {
			t.Skip("set FN2AUTH_SECURITY_EXPIRY=1 for actual five-minute preview expiration")
		}
		token := f.preview(t, users[0], alice, ciphertext)
		t.Log("waiting for the production five-minute token lifetime")
		deadline := time.Now().Add(5*time.Minute + 2*time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(min(time.Until(deadline), 30*time.Second))
		}
		result := f.confirm(t, users[0], alice, token, 409)
		if !strings.Contains(fmt.Sprint(result["code"]), "expired") {
			t.Fatal("unexpected expiry rejection")
		}
		f.checks["production_five_minute_expiry"] = true
	})
}
