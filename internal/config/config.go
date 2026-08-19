package config

import (
	"os"
	"path/filepath"
	"strconv"
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

func defaultDataDir() string {
	if v, err := os.UserConfigDir(); err == nil && v != "" {
		return filepath.Join(v, "burpsuite-clone")
	}
	return ".burpsuite-clone"
}
