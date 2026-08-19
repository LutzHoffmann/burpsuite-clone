package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/api"
	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/config"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/proxy"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
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
	queue := intercept.NewQueue(cfg.InterceptTimeout)
	interceptState := intercept.ControllerState{
		Enabled: false,
		Rules:   []intercept.Rule{{Enabled: true}},
	}
	if saved, err := history.GetSetting(context.Background(), "intercept.config"); err == nil {
		if err := json.Unmarshal([]byte(saved), &interceptState); err != nil {
			log.Printf("load intercept config: %v", err)
		}
	} else if err != sql.ErrNoRows {
		log.Printf("load intercept config: %v", err)
	}
	interceptController := intercept.NewController(queue, interceptState.Enabled, interceptState.Rules)

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
		Intercept:      interceptController,
	})
	go func() {
		if err := proxyServer.Serve(proxyListener); err != nil && err != http.ErrServerClosed {
			log.Printf("proxy server: %v", err)
		}
	}()

	apiServer := api.NewServer(api.Config{
		Store:        history,
		Authority:    authority,
		Events:       hub,
		Repeater:     repeater.NewService(nil, cfg.BodyLimitBytes),
		APIAddr:      cfg.APIAddr,
		ProxyAddr:    cfg.ProxyAddr,
		Intercept:    interceptController,
		MaxBodyBytes: cfg.BodyLimitBytes,
	})
	httpServer := &http.Server{
		Addr: cfg.APIAddr, Handler: apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute,
	}
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
