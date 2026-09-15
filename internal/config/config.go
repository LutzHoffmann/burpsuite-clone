package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	APIAddr          string
	ProxyAddr        string
	DataDir          string
	BodyLimitBytes   int64
	InterceptTimeout time.Duration
}

func Load() Config {
	cfg := Config{
		APIAddr:          "127.0.0.1:9080",
		ProxyAddr:        "127.0.0.1:8080",
		DataDir:          defaultDataDir(),
		BodyLimitBytes:   1048576,
		InterceptTimeout: 120 * time.Second,
	}
	if v := os.Getenv("BC_API_ADDR"); v != "" {
		cfg.APIAddr = v
	}
	if v := os.Getenv("BC_PROXY_ADDR"); v != "" {
		cfg.ProxyAddr = v
	}
	if v := os.Getenv("BC_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("BC_BODY_LIMIT_BYTES"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			cfg.BodyLimitBytes = parsed
		}
	}
	return cfg
}

// ValidateLocalListeners prevents accidentally exposing the unauthenticated control plane or proxy.
func ValidateLocalListeners(cfg Config) error {
	for name, address := range map[string]string{"API": cfg.APIAddr, "proxy": cfg.ProxyAddr} {
		host, port, err := net.SplitHostPort(address)
		if err != nil || port == "" {
			return fmt.Errorf("%s listener address %q is invalid", name, address)
		}
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("%s listener %q must use a loopback IP address", name, address)
		}
	}
	return nil
}

// EnsurePrivateDataDir creates the data directory and tightens permissions on existing directories.
func EnsurePrivateDataDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("data directory is empty")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect data directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("data directory %q is not a directory", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("restrict data directory permissions: %w", err)
	}
	return nil
}

func defaultDataDir() string {
	if v, err := os.UserConfigDir(); err == nil && v != "" {
		return filepath.Join(v, "burpsuite-clone")
	}
	return ".burpsuite-clone"
}
