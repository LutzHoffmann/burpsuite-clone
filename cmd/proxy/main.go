package main

import (
	"fmt"

	"github.com/lutzifer/burpsuite-clone/internal/config"
)

func main() {
	cfg := config.Load()
	fmt.Printf("api=%s proxy=%s data=%s\n", cfg.APIAddr, cfg.ProxyAddr, cfg.DataDir)
}
