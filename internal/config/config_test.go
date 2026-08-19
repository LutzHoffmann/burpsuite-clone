package config

import "testing"

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
