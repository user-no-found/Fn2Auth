// Package vault binds private state to the currently verified NAS identity.
package vault

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/user-no-found/FnPackKit/runtime/identity"
)

// Binding records observed identity attributes, not proof against unobserved UID reuse.
type Binding struct {
	UID  uint32 `json:"uid"`
	Name string `json:"name"`
	Home string `json:"home"`
	GID  uint32 `json:"gid"`
}

// State identifies one business user's private database and key.
type State struct {
	Directory, Key string
	User           identity.User
}

// Directory requires package-account ownership and private permissions without symlinks.
func Directory(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0700 {
		return fmt.Errorf("unsafe private directory: %s", path)
	}
	return nil
}

// ExclusiveFile writes a new private file and never replaces existing data.
func ExclusiveFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	return err
}

// Prepare initializes a new vault or refuses incomplete/foreign existing state.
// The caller holds the application lifecycle lock and serializes per-user startup.
func Prepare(parent string, user identity.User) (State, error) {
	state := State{Directory: filepath.Join(parent, strconv.FormatUint(uint64(user.UID), 10)), User: user}
	_, err := os.Lstat(state.Directory)
	fresh := errors.Is(err, os.ErrNotExist)
	if err != nil && !fresh {
		return state, err
	}
	if err = Directory(state.Directory); err != nil {
		return state, err
	}
	wanted := Binding{UID: user.UID, Name: user.Name, Home: user.Home, GID: user.GID}
	bindingPath := filepath.Join(state.Directory, "identity.json")
	if fresh {
		data, _ := json.Marshal(wanted)
		if err = ExclusiveFile(bindingPath, data); err != nil {
			return state, err
		}
		var random [32]byte
		if _, err = rand.Read(random[:]); err != nil {
			return state, err
		}
		state.Key = "base64:" + base64.StdEncoding.EncodeToString(random[:])
		if err = ExclusiveFile(filepath.Join(state.Directory, "app.key"), []byte(state.Key)); err != nil {
			return state, err
		}
		if err = ExclusiveFile(filepath.Join(state.Directory, "database.sqlite"), nil); err != nil {
			return state, err
		}
	} else {
		data, readErr := privateRead(bindingPath)
		if readErr != nil {
			return state, errors.New("vault identity missing or unsafe; manual recovery required")
		}
		var actual Binding
		if json.Unmarshal(data, &actual) != nil {
			return state, errors.New("invalid historical vault identity; manual recovery required")
		}
		if actual != wanted {
			Revoke(parent,user.UID)
			return state, errors.New("historical vault identity differs; access quarantined")
		}
		if _, err = os.Lstat(filepath.Join(state.Directory, "revoked")); err == nil {
			return state, errors.New("vault binding revoked; manual review required")
		}
		key, keyErr := privateRead(filepath.Join(state.Directory, "app.key"))
		if keyErr != nil {
			return state, errors.New("vault key missing or unsafe; refusing replacement")
		}
		state.Key = string(key)
		if len(state.Key) != 51 || state.Key[:7] != "base64:" {
			return state, errors.New("invalid stored application key")
		}
		if raw, decodeErr := base64.StdEncoding.Strict().DecodeString(state.Key[7:]); decodeErr != nil || len(raw) != 32 {
			return state, errors.New("invalid stored application key")
		}
		if _, err = privateRead(filepath.Join(state.Directory, "database.sqlite")); err != nil {
			return state, errors.New("vault database missing or unsafe; manual recovery required")
		}
	}
	for _, name := range []string{"storage", "storage/app", "storage/app/public", "storage/app/public/icons", "storage/app/temp", "storage/app/logos", "storage/app/imagesLink", "storage/app/iconPacks", "storage/framework", "storage/framework/cache", "storage/framework/cache/data", "storage/framework/sessions", "storage/framework/views", "storage/logs", "cache", "tmp"} {
		if err = Directory(filepath.Join(state.Directory, name)); err != nil {
			return state, err
		}
	}
	return state, nil
}

func privateRead(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	stat := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("unsafe private file")
	}
	if filepath.Base(path) == "database.sqlite" {
		return nil, nil
	}
	if info.Size() > 65536 {
		return nil, errors.New("private metadata too large")
	}
	return io.ReadAll(file)
}

// Revoke permanently marks an observed missing or mismatched account for review.
func Revoke(parent string, uid uint32) {
	directory := filepath.Join(parent, strconv.FormatUint(uint64(uid), 10))
	if info, err := os.Lstat(directory); err == nil && info.IsDir() {
		_ = ExclusiveFile(filepath.Join(directory, "revoked"), []byte("identity review required\n"))
	}
}

// Revoked reports a persistent quarantine marker, including malformed marker types.
func Revoked(parent string, uid uint32) bool {
	_, err := os.Lstat(filepath.Join(parent, strconv.FormatUint(uint64(uid), 10), "revoked"))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}
