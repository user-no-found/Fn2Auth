package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"filippo.io/age"
)

// Executor invokes the selected business user's final PHP validation/transaction.
type Executor func(context.Context, string, []byte) ([]byte, error)

type requestBody struct {
	Passphrase string `json:"passphrase"`
	Encrypted  string `json:"encrypted"`
	Token      string `json:"preview_token"`
	Confirm    bool   `json:"confirm"`
}

// Serve handles only explicit same-origin POST operations; there is no automatic retry.
func (manager *Manager) Serve(writer http.ResponseWriter, request *http.Request, action string, uid uint32, execute Executor) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if request.Method != "POST" || request.Header.Get("X-Fn2Auth-Origin") != "same-origin" {
		reject(writer, "backup_invalid_request", 400)
		return
	}
	cookies := request.CookiesNamed("fn2auth_" + strconv.FormatUint(uint64(uid), 10) + "_session")
	if len(cookies) != 1 || cookies[0].Value == "" {
		reject(writer, "session_required", 403)
		return
	}
	select {
	case manager.work <- struct{}{}:
		defer func() { <-manager.work }()
	default:
		reject(writer, "backup_unavailable", 503)
		return
	}
	session := sha256.Sum256([]byte(cookies[0].Value))
	decoder := json.NewDecoder(io.LimitReader(request.Body, 12*1024*1024+1))
	decoder.DisallowUnknownFields()
	var body requestBody
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
		reject(writer, "backup_invalid_request", 400)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
	defer cancel()
	if action == "import" {
		if !body.Confirm || body.Passphrase != "" || body.Encrypted != "" {
			reject(writer, "backup_invalid_request", 400)
			return
		}
		data, err := manager.consume(uid, session, body.Token)
		if err != nil {
			reject(writer, "preview_expired", 409)
			return
		}
		defer clear(data)
		result, err := execute(ctx, "import", data)
		if err != nil {
			reject(writer, "backup_unavailable", 503)
			return
		}
		writeJSON(writer, result)
		return
	}
	if utf8.RuneCountInString(body.Passphrase) < 10 || utf8.RuneCountInString(body.Passphrase) > 1024 || body.Token != "" || body.Confirm {
		reject(writer, "backup_invalid_request", 400)
		return
	}
	switch action {
	case "export":
		if body.Encrypted != "" {
			reject(writer, "backup_invalid_request", 400)
			return
		}
		data, err := execute(ctx, "export", nil)
		if err != nil || len(data) > maxBytes {
			reject(writer, "backup_unavailable", 503)
			return
		}
		defer clear(data)
		recipient, err := age.NewScryptRecipient(body.Passphrase)
		if err != nil {
			reject(writer, "backup_invalid_request", 400)
			return
		}
		recipient.SetWorkFactor(16)
		var encrypted bytes.Buffer
		stream, err := age.Encrypt(&encrypted, recipient)
		if err == nil {
			_, err = stream.Write(data)
		}
		if err == nil {
			err = stream.Close()
		}
		if err != nil || encrypted.Len() > maxBytes {
			reject(writer, "backup_unavailable", 503)
			return
		}
		if ctx.Err() != nil {
			return
		}
		writer.Header().Set("Content-Type", "application/octet-stream")
		writer.Header().Set("Content-Disposition", `attachment; filename="fn2auth-otp.age"`)
		_, _ = writer.Write(encrypted.Bytes())
	case "preview":
		ciphertext, err := base64.StdEncoding.Strict().DecodeString(body.Encrypted)
		if err != nil || len(ciphertext) == 0 || len(ciphertext) > maxBytes {
			reject(writer, "backup_invalid_request", 400)
			return
		}
		identity, err := age.NewScryptIdentity(body.Passphrase)
		if err != nil {
			reject(writer, "decryption_failed", 400)
			return
		}
		identity.SetMaxWorkFactor(16)
		stream, err := age.Decrypt(bytes.NewReader(ciphertext), identity)
		if err != nil {
			reject(writer, "decryption_failed", 400)
			return
		}
		data, err := io.ReadAll(io.LimitReader(stream, maxBytes+1))
		if err != nil || len(data) > maxBytes {
			clear(data)
			reject(writer, "decryption_failed", 400)
			return
		}
		result, err := execute(ctx, "preview", data)
		if err != nil {
			clear(data)
			reject(writer, "backup_invalid_request", 400)
			return
		}
		var summary map[string]any
		if json.Unmarshal(result, &summary) != nil {
			clear(data)
			reject(writer, "backup_unavailable", 503)
			return
		}
		if ctx.Err() != nil {
			clear(data)
			return
		}
		token, err := manager.issue(uid, session, data)
		if err != nil {
			clear(data)
			reject(writer, "backup_unavailable", 503)
			return
		}
		summary["schema"] = 1
		summary["preview_token"] = token
		summary["expires_in"] = int(lifetime / time.Second)
		encoded, _ := json.Marshal(summary)
		writeJSON(writer, encoded)
	default:
		reject(writer, "backup_invalid_request", 400)
	}
}

func writeJSON(writer http.ResponseWriter, data []byte) {
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write(data)
}
func reject(writer http.ResponseWriter, code string, status int) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{"schema": 1, "status": "rejected", "code": code})
}
