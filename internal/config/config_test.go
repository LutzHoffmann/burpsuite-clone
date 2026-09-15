package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadUsesLocalDefaults(t *testing.T) {
	t.Setenv("BC_API_ADDR", "")
	t.Setenv("BC_PROXY_ADDR", "")
	t.Setenv("BC_DATA_DIR", "")

	cfg := Load()

	if cfg.APIAddr != "127.0.0.1:9080" {
		t.Fatalf("APIAddr = %q", cfg.APIAddr)
	}
	if cfg.ProxyAddr != "127.0.0.1:8080" {
		t.Fatalf("ProxyAddr = %q", cfg.ProxyAddr)
	}
	if cfg.BodyLimitBytes != 1048576 {
		t.Fatalf("BodyLimitBytes = %d", cfg.BodyLimitBytes)
	}
	if cfg.InterceptTimeout.String() != "2m0s" {
		t.Fatalf("InterceptTimeout = %s", cfg.InterceptTimeout)
	}
}

func TestValidateLocalListenersRejectsNetworkExposure(t *testing.T) {
	tests := []Config{
		{APIAddr: "0.0.0.0:9080", ProxyAddr: "127.0.0.1:8080"},
		{APIAddr: "127.0.0.1:9080", ProxyAddr: "192.0.2.10:8080"},
		{APIAddr: ":9080", ProxyAddr: "127.0.0.1:8080"},
	}
	for _, cfg := range tests {
		if err := ValidateLocalListeners(cfg); err == nil {
			t.Fatalf("ValidateLocalListeners(%+v) succeeded", cfg)
		}
	}
}

func TestValidateLocalListenersAcceptsLoopback(t *testing.T) {
	for _, cfg := range []Config{
		{APIAddr: "127.0.0.1:9080", ProxyAddr: "127.0.0.1:8080"},
		{APIAddr: "[::1]:9080", ProxyAddr: "[::1]:8080"},
	} {
		if err := ValidateLocalListeners(cfg); err != nil {
			t.Fatalf("ValidateLocalListeners(%+v): %v", cfg, err)
		}
	}
}

func TestEnsurePrivateDataDirRestrictsExistingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful for Windows ACLs")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDataDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("data directory permissions = %o, want 700", got)
	}
}
