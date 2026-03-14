// Package main provides the standalone SMSC Gateway binary.
// It speaks SMPP on both sides: northbound (accepts connections from SMPP
// clients) and southbound (connects to real or mock SMSC). It provides
// MSISDN-based sticky routing so DLR and MO messages are always routed back
// to the client that last submitted for a given MSISDN.
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/idnteq/go-smsc/gateway"
)

func main() {
	logger, _ := zap.NewProduction()
	if os.Getenv("LOG_LEVEL") == "debug" {
		logger, _ = zap.NewDevelopment()
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := run(ctx, logger); err != nil {
		logger.Fatal("smsc-gateway failed", zap.Error(err))
	}
}

func run(ctx context.Context, logger *zap.Logger) error {
	cfg := gateway.LoadConfig()

	// 1. Metrics
	metrics := gateway.NewMetrics()

	// 2. Pebble message store
	store, err := gateway.NewMessageStore(cfg.DataDir, logger.Named("store"))
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	// 3. Router (server and pool set after creation)
	router := gateway.NewRouter(store, metrics, cfg, logger.Named("router"))

	// 4. Route config store (persists routes and pool configs in Pebble)
	routeConfig := gateway.NewRouteConfigStore(store)
	router.SetRouteConfig(routeConfig)

	// 5. Pool Manager (manages multiple named southbound pools)
	poolManager := gateway.NewPoolManager(router.HandleDeliver, logger.Named("pool-mgr"))
	router.SetPoolManager(poolManager)

	// Load persisted pool configs from Pebble and connect each.
	poolConfigs, _ := routeConfig.LoadAllPoolConfigs()
	for _, pc := range poolConfigs {
		if err := poolManager.Add(ctx, pc); err != nil {
			logger.Warn("failed to load pool from config",
				zap.String("pool", pc.Name),
				zap.Error(err),
			)
		}
	}

	// Bootstrap convenience: if no pools persisted in Pebble and GW_SMSC_HOST
	// is configured, create a "default" pool via PoolManager so it gets
	// persisted and can be managed via the admin UI afterwards.
	if len(poolConfigs) == 0 && cfg.SMSCHost != "" {
		defaultPoolCfg := &gateway.SouthboundPoolConfig{
			Name:             "default",
			Host:             cfg.SMSCHost,
			Port:             cfg.SMSCPort,
			SystemID:         cfg.SMSCSystemID,
			Password:         cfg.SMSCPassword,
			SourceAddr:       cfg.SMSCSourceAddr,
			Connections:      cfg.PoolConnections,
			WindowSize:       cfg.PoolWindowSize,
			BindMode:         cfg.BindMode,
			InterfaceVersion: cfg.SMPPVersion,
		}
		if err := poolManager.Add(ctx, defaultPoolCfg); err != nil {
			logger.Warn("bootstrap default pool failed (create pools via admin UI)",
				zap.Error(err),
			)
		} else {
			// Persist so subsequent startups load from Pebble.
			if err := routeConfig.SavePoolConfig(defaultPoolCfg); err != nil {
				logger.Warn("failed to persist bootstrap default pool config", zap.Error(err))
			}
			logger.Info("bootstrap: created default pool from env vars",
				zap.String("host", cfg.SMSCHost),
				zap.Int("port", cfg.SMSCPort),
			)
		}
	} else if len(poolConfigs) == 0 {
		logger.Info("No southbound pools configured — create pools via admin UI")
	}
	defer poolManager.Close()

	// Load route tables from Pebble.
	mtRoutes, _ := routeConfig.LoadAllMTRoutes()
	moRoutes, _ := routeConfig.LoadAllMORoutes()
	router.SetMTRoutes(mtRoutes)
	router.SetMORoutes(moRoutes)

	// 6. Northbound server
	server := gateway.NewServer(cfg, metrics, logger.Named("server"))
	server.SetRouter(router)
	router.SetServer(server)

	// 7. Start northbound listener
	if err := server.Start(); err != nil {
		return err
	}
	defer server.Stop()

	// 8. Forward worker pool (bounded goroutines for southbound submits)
	router.StartForwardWorkers(cfg.ForwardWorkers)

	// 9. Background workers
	go store.RunCleanup(ctx, cfg.CleanupInterval, cfg.MessageTTL, logger.Named("cleanup"))
	go router.RunRetryLoop(ctx, cfg.RetryInterval, cfg.MaxRetryAge)
	go router.RunMetricsUpdater(ctx)
	go router.CleanupCorrelations(ctx, 5*time.Minute)
	go router.RunSubmitRetryLoop(ctx, cfg.SubmitRetryInterval)
	go server.RunEnquireLink(ctx)
	go server.RunStaleChecker(ctx)

	// 10. Auth stores
	keyStore := gateway.NewAPIKeyStore(store)
	jwtSecret := []byte(cfg.JWTSecret)
	userStore := gateway.NewAdminUserStore(store, jwtSecret)
	if err := userStore.Bootstrap(); err != nil {
		logger.Warn("admin bootstrap error", zap.Error(err))
	}
	connConfigStore := gateway.NewConnectionConfigStore(store)
	server.SetConnConfigStore(connConfigStore)

	// 11. HTTP server: REST API + Admin API + Admin UI
	httpMux := http.NewServeMux()

	// REST API endpoints (/api/v1/*)
	router.RegisterRESTRoutes(httpMux, keyStore)

	// Admin API endpoints (/admin/api/*)
	adminAPI := gateway.NewAdminAPI(router, poolManager, routeConfig, keyStore, userStore, connConfigStore, metrics, server, logger.Named("admin"))
	adminAPI.RegisterRoutes(httpMux)

	// Admin UI (catch-all for /admin/*)
	httpMux.Handle("/admin/", gateway.AdminUIHandler())
	// Root redirect to admin UI
	httpMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/admin/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	// Callback retry loop
	go router.RunCallbackRetryLoop(ctx, 10*time.Second)

	// Start HTTP server
	go func() {
		httpAddr := cfg.HTTPAddr
		srv := &http.Server{
			Addr:         httpAddr,
			Handler:      httpMux,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
		}

		// Use HTTP TLS if configured, fall back to SMPP TLS cert
		certFile := cfg.HTTPTLSCertFile
		keyFile := cfg.HTTPTLSKeyFile
		if certFile == "" {
			certFile = cfg.TLSCertFile
		}
		if keyFile == "" {
			keyFile = cfg.TLSKeyFile
		}

		if certFile != "" && keyFile != "" {
			logger.Info("HTTP server starting with TLS",
				zap.String("addr", httpAddr),
			)
			if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTP server error", zap.Error(err))
			}
		} else {
			logger.Info("HTTP server starting",
				zap.String("addr", httpAddr),
			)
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("HTTP server error", zap.Error(err))
			}
		}
	}()

	// 12. Prometheus metrics endpoint
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		srv := &http.Server{
			Addr:         cfg.MetricsAddr,
			Handler:      mux,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
		}
		logger.Info("metrics server starting", zap.String("addr", cfg.MetricsAddr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server error", zap.Error(err))
		}
	}()

	logger.Info("SMSC Gateway running",
		zap.String("northbound", cfg.ListenAddr),
		zap.String("southbound", cfg.SMSCHost),
		zap.String("http", cfg.HTTPAddr),
		zap.Int("pool_connections", cfg.PoolConnections),
		zap.Int("forward_workers", cfg.ForwardWorkers),
	)

	// 13. Wait for shutdown
	<-ctx.Done()
	logger.Info("SMSC Gateway shutdown complete")
	return nil
}
