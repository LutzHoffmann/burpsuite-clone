package main

import (
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/lutzifer/burpsuite-clone/internal/api"
	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/config"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/proxy"
	"github.com/lutzifer/burpsuite-clone/internal/store"
)

func main() {
	cfg := config.Load()
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		log.Fatal(err)
	}

	history, err := store.OpenSQLite(filepath.Join(cfg.DataDir, "project.sqlite"))
	if err != nil {
		log.Fatal(err)
	}
	defer history.Close()

	authority, err := certs.LoadOrCreateAuthority(filepath.Join(cfg.DataDir, "ca"))
	if err != nil {
		log.Fatal(err)
	}
	hub := events.NewHub()

	proxyListener, err := net.Listen("tcp", cfg.ProxyAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer proxyListener.Close()
	proxyServer := proxy.NewServer(proxy.Config{
		Store:          history,
		BodyLimitBytes: cfg.BodyLimitBytes,
		Authority:      authority,
		Events:         hub,
	})
	go func() {
		if err := proxyServer.Serve(proxyListener); err != nil && err != http.ErrServerClosed {
			log.Printf("proxy server: %v", err)
		}
	}()

	apiServer := api.NewServer(api.Config{
		Store:     history,
		Authority: authority,
		Events:    hub,
		APIAddr:   cfg.APIAddr,
		ProxyAddr: cfg.ProxyAddr,
	})
	if err := http.ListenAndServe(cfg.APIAddr, apiServer.Handler()); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
