package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gpuhub/internal/api"
	"gpuhub/internal/auth"
	"gpuhub/internal/config"
	"gpuhub/internal/crypto"
	"gpuhub/internal/db"
	"gpuhub/internal/jobs"
	"gpuhub/internal/providers"
	"gpuhub/internal/proxy"
	"gpuhub/web"
)

func main() {
	// --migrate-only: open the DB (applying migrations) and exit.
	for _, a := range os.Args[1:] {
		if a == "--migrate-only" {
			if os.Getenv("GPUHUB_MASTER_KEY") == "" {
				_ = os.Setenv("GPUHUB_DEV", "1") // migrations don't need the master key
			}
			cfg, err := config.Load()
			if err != nil {
				log.Fatalf("config: %v", err)
			}
			conn, err := db.Open(cfg.DatabasePath)
			if err != nil {
				log.Fatalf("db: %v", err)
			}
			_ = conn.Close()
			log.Printf("migrations applied to %s", cfg.DatabasePath)
			return
		}
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("GPUHub starting (port=%d, db=%s)", cfg.Port, cfg.DatabasePath)

	conn, err := db.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer conn.Close()

	box, err := crypto.NewBox(cfg.MasterKey)
	if err != nil {
		log.Fatalf("crypto: %v", err)
	}

	// Sticky proxy transport cache — adapters get their HTTP clients exclusively through here.
	proxyMgr := proxy.NewManager(conn)
	proxyMgr.Decrypt = box.Decrypt
	providers.BindProxyManager(proxyMgr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	notify := func(level, msg string) {
		_, _ = conn.ExecContext(context.Background(), `INSERT INTO notifications(level, message) VALUES(?,?)`, level, msg)
		log.Printf("[notify:%s] %s", level, msg)
		if cfg.NotifyWebhookURL != "" {
			go func() {
				body, _ := json.Marshal(map[string]string{"level": level, "message": msg, "source": "gpuhub"})
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				req, err := http.NewRequestWithContext(c, http.MethodPost, cfg.NotifyWebhookURL, bytes.NewReader(body))
				if err != nil {
					return
				}
				req.Header.Set("Content-Type", "application/json")
				_, _ = http.DefaultClient.Do(req)
			}()
		}
	}

	// Seed provider catalog + bootstrap admin from env (first run only).
	apiServer := api.NewServer(cfg, conn, box, auth.NewService(cfg.JWTSecret), proxyMgr, mustWebFS())
	apiServer.SeedProviders(ctx)
	if created, err := apiServer.BootstrapAdmin(ctx); err != nil {
		log.Fatalf("admin bootstrap: %v", err)
	} else if created {
		log.Printf("admin user %q created from environment", cfg.AdminUsername)
	}

	// Background workers: scheduler + poller + proxy health checks (60s).
	scheduler := jobs.New(conn, cfg, box, notify)
	scheduler.Start(ctx)
	go proxyMgr.HealthLoop(ctx, cfg.ProxyHealthInterval, cfg.ProxyHealthURL, cfg.ProxyHealthTimeout, notify)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // WebSocket/log streaming
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("GPUHub listening on http://0.0.0.0:%d  (UI: /, docs: /docs)", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		log.Fatalf("server: %v", err)
	case s := <-sig:
		log.Printf("received %s, shutting down", s)
		shutdownCtx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
		_ = srv.Shutdown(shutdownCtx)
		cancel2()
	}
}

func mustWebFS() fs.FS {
	f, err := web.FS()
	if err != nil {
		log.Printf("warning: web dist not available: %v", err)
		return nil
	}
	return f
}
