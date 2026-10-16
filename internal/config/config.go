// Package config owns Fn2Auth's editable deployment settings.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/user-no-found/FnPackKit/runtime/gatewaypolicy"
)

// Config is loaded once by the application service; changes require restart.
type Config struct {
	AppDir                 string              `json:"app_dir"`
	StateDir               string              `json:"state_dir"`
	RunDir                 string              `json:"run_dir"`
	Prefix                 string              `json:"prefix"`
	TrustedGatewayAccounts []string            `json:"trusted_gateway_accounts"`
	AllowedHosts           []string            `json:"allowed_hosts"`
	AllowedOrigins         []string            `json:"allowed_origins"`
	ExternalOriginPorts    map[string][]uint16 `json:"external_origin_ports"`
	AllowSameOriginFetch   bool                `json:"allow_same_origin_fetch"`
	MaxUsers               int                 `json:"max_users"`
	PHPThreads             int                 `json:"php_threads"`
	PHPMemoryMB            int                 `json:"php_memory_mb"`
	RequestBodyMB          int64               `json:"request_body_mb"`
	StartupSeconds         int                 `json:"startup_seconds"`
	ShutdownSeconds        int                 `json:"shutdown_seconds"`
	IdleSeconds            int                 `json:"idle_seconds"`
	ConnectionSeconds      int                 `json:"connection_seconds"`
	Locale                 string              `json:"locale"`
}

// Load rejects unknown fields, invalid limits and ambiguous deployment paths.
func Load(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if len(data) > 65536 {
		return cfg, errors.New("configuration exceeds 64 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("configuration: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return cfg, errors.New("configuration has trailing data")
	}
	for _, p := range []string{cfg.AppDir, cfg.StateDir, cfg.RunDir} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" {
			return cfg, errors.New("deployment paths must be clean absolute directories")
		}
	}
	if cfg.AppDir == cfg.StateDir || cfg.AppDir == cfg.RunDir || cfg.StateDir == cfg.RunDir {
		return cfg, errors.New("deployment directories must differ")
	}
	paths := []string{cfg.AppDir, cfg.StateDir, cfg.RunDir}
	for i, parent := range paths {
		for j, child := range paths {
			if i != j && strings.HasPrefix(child, parent+string(filepath.Separator)) {
				return cfg, errors.New("deployment directories must not contain one another")
			}
		}
	}
	if len(cfg.TrustedGatewayAccounts) == 0 {
		return cfg, errors.New("trusted_gateway_accounts must be explicit")
	}
	if cfg.MaxUsers < 1 || cfg.MaxUsers > 64 || cfg.PHPThreads < 1 || cfg.PHPThreads > 16 || cfg.PHPMemoryMB < 64 || cfg.PHPMemoryMB > 2048 || cfg.RequestBodyMB < 1 || cfg.RequestBodyMB > 64 {
		return cfg, errors.New("invalid resource limits")
	}
	if cfg.StartupSeconds < 5 || cfg.StartupSeconds > 300 || cfg.ShutdownSeconds < 5 || cfg.ShutdownSeconds > 300 || cfg.IdleSeconds < 60 || cfg.IdleSeconds > 86400 || cfg.ConnectionSeconds < 60 || cfg.ConnectionSeconds > 86400 {
		return cfg, errors.New("invalid lifecycle timeouts")
	}
	if cfg.Locale != "zh-CN" && cfg.Locale != "zh-TW" && cfg.Locale != "en" {
		return cfg, errors.New("locale must be zh-CN, zh-TW or en")
	}
	if _, err = gatewaypolicy.New(cfg.GatewayPolicy()); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// GatewayPolicy supplies the shared transport and browser origin boundary.
func (cfg Config) GatewayPolicy() gatewaypolicy.Config {
	return gatewaypolicy.Config{Prefix: cfg.Prefix, AllowedHosts: cfg.AllowedHosts, AllowedOrigins: cfg.AllowedOrigins,
		ExternalOriginPorts: cfg.ExternalOriginPorts, AllowSameOriginFetch: cfg.AllowSameOriginFetch}
}

// StartupTimeout is the bounded native worker initialization interval.
func (cfg Config) StartupTimeout() time.Duration {
	return time.Duration(cfg.StartupSeconds) * time.Second
}
