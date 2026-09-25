package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vitlane/vitlane/server/internal/shared/iface/httpapi"
	sharedhttpclient "github.com/vitlane/vitlane/server/internal/shared/infra/httpclient"
	sharedpostgres "github.com/vitlane/vitlane/server/internal/shared/infra/postgres"
	"github.com/vitlane/vitlane/server/internal/shared/runtimepolicy"
)

var sourceRevision string

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if sourceRevision != "" {
		logger.Info("server build", "event", "server.build", "source_revision", sourceRevision)
	}
	commandContext := context.Background()
	if len(os.Args) > 1 && os.Args[1] == "paypal-binding" {
		if err := runPayPalBindingCommand(commandContext, os.Args[2:], logger); err != nil {
			logger.Error("paypal-binding failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "live-control" {
		if err := runLiveControlCommand(commandContext, os.Args[2:], logger); err != nil {
			logger.Error("live-control failed", "error", err)
			os.Exit(1)
		}
		return
	}
	if err := run(logger); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	config, err := loadConfig()
	if err != nil {
		return err
	}
	databaseURL := env("DATABASE_URL", "postgres://vitlane:vitlane@127.0.0.1:5432/vitlane?sslmode=disable")
	database, err := sharedpostgres.OpenWithConfig(
		ctx, databaseURL, config.runtimePolicies.database,
	)
	if err != nil {
		return err
	}
	defer database.Close()

	if env("MIGRATE_ON_START", "true") == "true" {
		migrationContext := runtimepolicy.WithWorkClass(ctx, runtimepolicy.Admin)
		migrationContext, cancelMigration := runtimepolicy.WithTimeout(
			migrationContext, config.runtimePolicies.database.Admin.Transaction,
		)
		defer cancelMigration()
		if err := database.Migrate(migrationContext, env("MIGRATIONS_DIR", "migrations")); err != nil {
			return err
		}
	}
	transport, err := sharedhttpclient.NewTransport(config.runtimePolicies.httpTransport)
	if err != nil {
		return err
	}
	defer transport.CloseIdleConnections()
	databaseStats := database.Stats()
	logger.Info("runtime policies configured",
		"event", "server.runtime_policy_configured",
		"database", slog.GroupValue(
			slog.Int("max_open", databaseStats.MaxOpenConnections),
			slog.Int("max_idle", config.runtimePolicies.database.MaxIdleConnections),
			slog.String("connection_max_idle", config.runtimePolicies.database.ConnMaxIdleTime.String()),
			slog.String("connection_lifetime", config.runtimePolicies.database.ConnMaxLifetime.String()),
			slog.String("acquire_timeout", config.runtimePolicies.database.AcquireTimeout.String()),
			slog.String("migration_lock_timeout", config.runtimePolicies.database.MigrationLockTimeout.String()),
			slog.Any("interactive", timeoutClassLogValue(config.runtimePolicies.database.Interactive)),
			slog.Any("worker", timeoutClassLogValue(config.runtimePolicies.database.Worker)),
			slog.Any("migration_admin", timeoutClassLogValue(config.runtimePolicies.database.Admin)),
		),
		"http_transport", slog.GroupValue(
			slog.String("connect_timeout", config.runtimePolicies.httpTransport.ConnectTimeout.String()),
			slog.String("keep_alive", config.runtimePolicies.httpTransport.KeepAlive.String()),
			slog.String("tls_handshake_timeout", config.runtimePolicies.httpTransport.TLSHandshakeTimeout.String()),
			slog.String("response_header_timeout", config.runtimePolicies.httpTransport.ResponseHeaderTimeout.String()),
			slog.String("expect_continue_timeout", config.runtimePolicies.httpTransport.ExpectContinueTimeout.String()),
			slog.String("idle_connection_timeout", config.runtimePolicies.httpTransport.IdleConnectionTimeout.String()),
			slog.Int("max_idle", config.runtimePolicies.httpTransport.MaxIdleConnections),
			slog.Int("max_idle_per_host", config.runtimePolicies.httpTransport.MaxIdleConnectionsHost),
			slog.Int("max_connections_per_host", config.runtimePolicies.httpTransport.MaxConnectionsHost),
		),
		"http_ingress", slog.GroupValue(
			slog.Int("max_concurrent_requests", config.runtimePolicies.maxConcurrentRequests),
		),
		"timeouts", slog.GroupValue(
			slog.String("request", config.runtimePolicies.requestTimeout.String()),
			slog.String("external_finalization", config.runtimePolicies.externalFinalize.String()),
			slog.String("google_oidc", config.runtimePolicies.googleOIDCTimeout.String()),
			slog.String("shopify", config.runtimePolicies.shopifyTimeout.String()),
			slog.String("managed_model", config.runtimePolicies.managedModelTimeout.String()),
			slog.String("evm_rpc_http", config.runtimePolicies.evmHTTPClientTimeout.String()),
		),
	)

	app, err := newApplication(ctx, config, database, transport, logger)
	if err != nil {
		return err
	}
	defer app.Close()
	handler, err := newRouter(app)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr: env("HTTP_ADDR", ":8080"),
		// Metrics wrap outermost so middleware-written responses (timeouts,
		// panics) are counted in the ops health 5xx readback too.
		Handler: app.httpMetrics.Wrap(httpapi.Middleware(
			httpapi.LimitConcurrentRequests(
				handler, config.runtimePolicies.maxConcurrentRequests,
			),
			app.ids, logger, config.runtimePolicies.requestTimeout, findingImportTimeout,
		)),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	startWorkers(ctx, app)
	errChannel := make(chan error, 1)
	go func() {
		logger.Info("server started", "address", server.Addr)
		errChannel <- server.ListenAndServe()
	}()

	// ADR-0040 §6: the host watch timer reads the same ops health report
	// through a loopback-only listener inside the container network
	// namespace (docker exec), so no operator session or new secret is
	// needed and nothing is exposed outside the container. Opt-in via env;
	// the production Compose sets it, dev and E2E stacks leave it off.
	var internalServer *http.Server
	if internalAddr := env("OPS_INTERNAL_HTTP_ADDR", ""); internalAddr != "" {
		internalMux := http.NewServeMux()
		internalMux.Handle(
			"GET /internal/ops/health", newOpsHealthHandler(app.opsReporter),
		)
		internalServer = &http.Server{
			Addr:              internalAddr,
			Handler:           internalMux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      15 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			logger.Info("internal ops listener started", "address", internalAddr)
			if err := internalServer.ListenAndServe(); err != nil &&
				!errors.Is(err, http.ErrServerClosed) {
				errChannel <- fmt.Errorf("internal ops listener: %w", err)
			}
		}()
	}

	select {
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if internalServer != nil {
			_ = internalServer.Shutdown(shutdownContext)
		}
		return server.Shutdown(shutdownContext)
	case err := <-errChannel:
		// Either listener failing ends the process; the exit tears the other
		// one down, so no extra shutdown context is created here (ADR-0039
		// allows exactly the signal root and the bounded shutdown above).
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func timeoutClassLogValue(class sharedpostgres.TimeoutClass) slog.Value {
	return slog.GroupValue(
		slog.String("query", class.Query.String()),
		slog.String("transaction", class.Transaction.String()),
		slog.String("lock", class.Lock.String()),
	)
}
