package instance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Backup executes the private PHP OTP transaction protocol without shell or secret argv.
func (worker *Worker) Backup(ctx context.Context, action string, input []byte) ([]byte, error) {
	if action != "export" && action != "preview" && action != "import" {
		return nil, errors.New("invalid backup operation")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, worker.php, "php-cli", filepath.Join(worker.app, "bootstrap/fn2auth-backup.php"), action)
	command.Env = worker.environment
	command.Dir = worker.app
	command.Stdin = bytes.NewReader(input)
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = command.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(output, 8*1024*1024+1))
	if readErr != nil || len(data) > 8*1024*1024 {
		cancel()
		_ = command.Wait()
		return nil, errors.New("backup output exceeds limit")
	}
	if err = command.Wait(); err != nil {
		return nil, errors.New("backup transaction failed")
	}
	return data, nil
}
