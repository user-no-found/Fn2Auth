// Command fn2auth runs the authenticated application and its fnOS lifecycle commands.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"fn2auth/internal/config"
	"fn2auth/internal/service"
	"fn2auth/internal/vault"
)

var version, displayName, buildID string

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: fn2auth configure|start|stop|status|serve|version")
	}
	if os.Args[1] == "version" {
		fmt.Println(version)
		return nil
	}
	flags := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	filename := flags.String("config", "", "editable deployment configuration")
	template := flags.String("template", "", "packaged deployment configuration template")
	appDir := flags.String("app-dir", "", "package program directory")
	stateDir := flags.String("state-dir", "", "persistent state directory")
	runDir := flags.String("run-dir", "", "temporary runtime directory")
	readyFD := flags.Int("ready-fd", -1, "internal readiness pipe")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if *filename == "" || !filepath.IsAbs(*filename) {
		return errors.New("--config requires an absolute path")
	}
	if os.Args[1] == "configure" {
		if _, err := os.Stat(*filename); err == nil {
			_, err = config.Load(*filename)
			return err
		}
		data, err := os.ReadFile(*template)
		if err != nil {
			return err
		}
		for token, value := range map[string]string{"@app_dir@": *appDir, "@state_dir@": *stateDir, "@run_dir@": *runDir} {
			encoded, _ := json.Marshal(value)
			data = bytes.ReplaceAll(data, []byte(`"`+token+`"`), encoded)
		}
		if err = vault.ExclusiveFile(*filename, data); err != nil {
			return err
		}
		_, err = config.Load(*filename)
		return err
	}
	cfg, err := config.Load(*filename)
	if err != nil {
		return err
	}
	switch os.Args[1] {
	case "status":
		os.Exit(service.Status(cfg))
	case "start":
		return service.Start(cfg, *filename)
	case "stop":
		return service.Stop(cfg)
	case "serve":
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer cancel()
		var ready *os.File
		if *readyFD >= 3 {
			ready = os.NewFile(uintptr(*readyFD), "readiness")
			defer ready.Close()
		}
		// A nil interface must remain nil when serve was started directly.
		meta := service.Metadata{Name: displayName, Version: version, Build: buildID}
		if ready != nil {
			return service.Serve(ctx, cfg, meta, ready)
		}
		return service.Serve(ctx, cfg, meta, nil)
	}
	return errors.New("unknown lifecycle command")
}
