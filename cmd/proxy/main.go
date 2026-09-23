package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lutzifer/burpsuite-clone/internal/api"
	"github.com/lutzifer/burpsuite-clone/internal/certs"
	"github.com/lutzifer/burpsuite-clone/internal/config"
	"github.com/lutzifer/burpsuite-clone/internal/events"
	"github.com/lutzifer/burpsuite-clone/internal/intercept"
	"github.com/lutzifer/burpsuite-clone/internal/intruder"
	"github.com/lutzifer/burpsuite-clone/internal/proxy"
	"github.com/lutzifer/burpsuite-clone/internal/repeater"
	"github.com/lutzifer/burpsuite-clone/internal/scope"
	"github.com/lutzifer/burpsuite-clone/internal/store"
	"github.com/lutzifer/burpsuite-clone/internal/target"
	"github.com/lutzifer/burpsuite-clone/internal/wsrepeater"
)

func main() {
	cfg := config.Load()
	if err := config.ValidateLocalListeners(cfg); err != nil {
		log.Fatal(err)
	}
	if err := config.EnsurePrivateDataDir(cfg.DataDir); err != nil {
		log.Fatal(err)
	}

	history, err := store.OpenSQLite(filepath.Join(cfg.DataDir, "project.sqlite"))
	if err != nil {
		log.Fatal(err)
	}
	defer history.Close()
	scopeState, err := history.LoadScopeState(context.Background())
	if err != nil {
		log.Fatalf("load scope state: %v", err)
	}
	compiledScope, err := scope.Compile(scopeState.Version, scopeState.Rules)
	if err != nil {
		log.Fatalf("compile scope rules: %v", err)
	}
	scopeManager := scope.NewManager(compiledScope)
	hub := events.NewHub()
	intruderService, err := intruder.NewService(context.Background(), history, intruderScope{scopeManager}, repeater.NewHTTPSender(nil), hub)
	if err != nil {
		log.Fatalf("recover Intruder jobs: %v", err)
	}
	defer intruderService.Close()
	targetService := target.NewService(history, scopeManager, hub, target.Limits{
		MaxJSONDepth: 16, MaxFields: 1000, MaxMultipartFields: 100,
	})
	if err := targetService.Recover(context.Background()); err != nil {
		log.Fatalf("recover target service: %v", err)
	}
	defer targetService.Close()

	authority, err := certs.LoadOrCreateAuthority(filepath.Join(cfg.DataDir, "ca"))
	if err != nil {
		log.Fatal(err)
	}
	queue := intercept.NewQueue(cfg.InterceptTimeout)
	interceptController := intercept.NewController(queue, false, []intercept.Rule{{Enabled: true}})
	if err := restoreInterceptConfig(context.Background(), history, interceptController); err != nil {
		log.Printf("load intercept config: %v", err)
	}

	proxyListener, err := net.Listen("tcp", cfg.ProxyAddr)
	if err != nil {
		log.Fatal(err)
	}
	defer proxyListener.Close()
	if err := history.RecoverWSConnections(context.Background()); err != nil {
		log.Fatalf("recover WebSocket history: %v", err)
	}
	proxyServer := proxy.NewServer(proxy.Config{
		Store:          history,
		BodyLimitBytes: cfg.BodyLimitBytes,
		Authority:      authority,
		Events:         hub,
		Intercept:      interceptController,
		Scope:          scopeManager,
		Target:         targetService,
	})
	go func() {
		if err := proxyServer.Serve(proxyListener); err != nil && err != http.ErrServerClosed {
			log.Printf("proxy server: %v", err)
		}
	}()

	wsSessions := wsrepeater.NewSessionManager(scopeManager, cfg.BodyLimitBytes)
	defer wsSessions.Close()
	apiServer := api.NewServer(api.Config{
		Store:        history,
		Authority:    authority,
		Events:       hub,
		Repeater:     repeater.NewService(nil, cfg.BodyLimitBytes),
		WSRepeater:   wsrepeater.NewService(scopeManager, cfg.BodyLimitBytes),
		WSSessions:   wsSessions,
		APIAddr:      cfg.APIAddr,
		ProxyAddr:    cfg.ProxyAddr,
		Intercept:    interceptController,
		Target:       targetService,
		Intruder:     intruderService,
		MaxBodyBytes: cfg.BodyLimitBytes,
	})
	httpServer := &http.Server{
		Addr: cfg.APIAddr, Handler: apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 2 * time.Minute, IdleTimeout: 2 * time.Minute,
	}
	shutdown, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serverDone := make(chan struct{})
	defer close(serverDone)
	go func() {
		select {
		case <-shutdown.Done():
			wsSessions.Close()
			_ = httpServer.Close()
		case <-serverDone:
		}
	}()
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		wsSessions.Close()
		log.Fatal(err)
	}
}

type intruderScope struct{ manager *scope.Manager }

func (s intruderScope) Allows(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	return s.manager.Current().Classify(scope.Target{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).InScope
}

func restoreInterceptConfig(ctx context.Context, settings interface {
	GetSetting(context.Context, string) (string, error)
}, controller *intercept.Controller) error {
	saved, err := settings.GetSetting(ctx, "intercept.config")
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	// Decode into defaults so older request-only settings keep response defaults.
	state := controller.State()
	if err := json.Unmarshal([]byte(saved), &state); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if err := intercept.ValidateState(state); err != nil {
		return fmt.Errorf("validate: %w", err)
	}
	controller.Update(state)
	return nil
}
