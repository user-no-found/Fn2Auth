package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"fn2auth/internal/config"
	"fn2auth/internal/vault"
)

func initialize(ctx context.Context, cfg config.Config, state vault.State, php, app string, environment []string, version string) error {
	marker := filepath.Join(state.Directory, "schema.json")
	failed := filepath.Join(state.Directory, "migration-failed.json")
	pending := filepath.Join(state.Directory, "migration-in-progress.json")
	if _, err := os.Lstat(pending); err == nil {
		return errors.New("interrupted vault migration; manual recovery required")
	}
	if _, err := os.Lstat(failed); err == nil {
		return errors.New("vault migration failed previously; manual recovery required")
	}
	var previous struct {
		Build string `json:"build"`
	}
	if data, err := os.ReadFile(marker); err == nil {
		if json.Unmarshal(data, &previous) != nil || previous.Build == "" {
			return errors.New("invalid migration marker")
		}
		if previous.Build == version {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	startRecord, _ := json.Marshal(map[string]string{"build": version, "status": "in-progress"})
	if err := vault.ExclusiveFile(pending, startRecord); err != nil {
		return err
	}
	if previous.Build != "" {
		if err := snapshot(ctx, state, php, app, environment, version); err != nil {
			return err
		}
	}
	log, err := os.OpenFile(filepath.Join(state.Directory, "storage/logs/migrate.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	commands := [][]string{{"package:discover", "--no-interaction"}, {"migrate", "--force", "--no-interaction"}}
	if _, err = os.Stat(filepath.Join(state.Directory, "storage/oauth-private.key")); errors.Is(err, os.ErrNotExist) {
		commands = append(commands, []string{"passport:keys", "--no-interaction"})
	}
	for _, args := range commands {
		command := exec.CommandContext(ctx, php, append([]string{"php-cli", filepath.Join(app, "artisan")}, args...)...)
		command.Env = environment
		command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
		command.Dir = app
		command.Stdout = log
		command.Stderr = log
		if err = command.Run(); err != nil {
			data, _ := json.Marshal(map[string]string{"build": version, "stage": args[0], "status": "failed"})
			_ = vault.ExclusiveFile(failed, data)
			return fmt.Errorf("private vault migration failed at %s", args[0])
		}
	}
	data, _ := json.Marshal(map[string]string{"build": version, "status": "ready"})
	temporary := marker + ".pending"
	if err = os.WriteFile(temporary, data, 0600); err != nil {
		return err
	}
	if err = os.Rename(temporary, marker); err != nil {
		return err
	}
	return os.Remove(pending)
}

func snapshot(ctx context.Context, state vault.State, php, app string, environment []string, version string) error {
	root := filepath.Join(state.Directory, "snapshots")
	if err := vault.Directory(root); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(root, "before-migration-")
	if err != nil {
		return err
	}
	// No PHP worker is running for this user yet. VACUUM INTO creates a consistent SQLite snapshot.
	command := exec.CommandContext(ctx, php, "php-cli", filepath.Join(app, "bootstrap/fn2auth-snapshot.php"))
	command.Env = append(append([]string(nil), environment...), "FN2AUTH_SNAPSHOT="+filepath.Join(directory, "database.sqlite"))
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if err = command.Run(); err != nil {
		return errors.New("pre-migration database snapshot failed")
	}
	if err = vault.ExclusiveFile(filepath.Join(directory, "app.key"), []byte(state.Key)); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]string{"target_build": version, "created_at": time.Now().UTC().Format(time.RFC3339), "scope": "database and matching APP_KEY; not a full storage backup"})
	return vault.ExclusiveFile(filepath.Join(directory, "snapshot.json"), data)
}
