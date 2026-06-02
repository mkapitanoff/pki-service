package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/lib/pq"

	"github.com/mkapitanoff/pki-service/internal/auth"
	"github.com/mkapitanoff/pki-service/internal/config"
	"github.com/mkapitanoff/pki-service/internal/handler"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/service"
	"github.com/mkapitanoff/pki-service/internal/storage"
	"github.com/mkapitanoff/pki-service/internal/worker"
)

func main() {
	cfg, err := config.Load(os.Getenv("APP_ENV"))
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	db, err := sql.Open("postgres", cfg.Database.DSN)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()
	if cfg.Database.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	}
	if cfg.Database.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	}
	if cfg.Database.ConnMaxLifetimeSec > 0 {
		db.SetConnMaxLifetime(time.Duration(cfg.Database.ConnMaxLifetimeSec) * time.Second)
	}
	if err := db.Ping(); err != nil {
		log.Fatalf("db ping: %v", err)
	}

	queries := repository.New(db)

	ncClient := ncanode.NewHTTPClient(ncanode.Options{
		URL:     cfg.NCANode.URL,
		Timeout: time.Duration(cfg.NCANode.TimeoutSec) * time.Second,
	})

	store, err := storage.New(storage.StorageConfig{
		Endpoint:     cfg.Storage.Endpoint,
		Region:       cfg.Storage.Region,
		Bucket:       cfg.Storage.Bucket,
		AccessKey:    cfg.Storage.AccessKey,
		SecretKey:    cfg.Storage.SecretKey,
		UsePathStyle: cfg.Storage.UsePathStyle,
	})
	if err != nil {
		log.Fatalf("storage: %v", err)
	}

	authSvc := auth.NewAuthService(queries, cfg.App.JWTSecret)
	authHandler := handler.NewAuthHandler(authSvc)

	signSvc := service.NewSignService(db, ncClient, store, queries, nil, cfg.App.VerifyBaseURL)
	signHandler := handler.NewSignHandler(signSvc, queries)
	verifyHandler := handler.NewVerifyHandler(queries)
	demoHandler := handler.NewDemoHandler(queries, store)
	documentHandler := handler.NewDocumentHandler(queries, store, cfg.App.VerifyBaseURL, ncClient)
	batchHandler := handler.NewBatchHandler(signSvc, queries, store, cfg.App.VerifyBaseURL, ncClient)
	adminHandler := handler.NewAdminHandler(queries, authSvc)

	extS3 := s3client.NewHTTPExternalS3Client()
	appHandler := handler.NewApplicationHandler(queries, signSvc, extS3, store)

	// Workers.
	workerCtx, cancelWorkers := context.WithCancel(context.Background())
	appCfg := cfg.Applications
	fetchInterval := time.Duration(appCfg.FetchIntervalSec) * time.Second
	if fetchInterval <= 0 {
		fetchInterval = 10 * time.Second
	}
	webhookInterval := time.Duration(appCfg.WebhookIntervalSec) * time.Second
	if webhookInterval <= 0 {
		webhookInterval = 5 * time.Second
	}
	docFetcher := worker.NewDocumentFetcher(worker.DocumentFetcherConfig{
		FetchInterval:   fetchInterval,
		FetchBatchSize:  appCfg.FetchBatchSize,
		MaxFetchRetries: appCfg.MaxFetchRetries,
	}, db, queries, store, extS3)
	webhookDispatcher := worker.NewWebhookDispatcher(worker.WebhookDispatcherConfig{
		Interval:    webhookInterval,
		MaxAttempts: appCfg.WebhookMaxAttempts,
	}, db, queries)
	go docFetcher.Run(workerCtx)
	go webhookDispatcher.Run(workerCtx)
	_ = cancelWorkers // called on shutdown below

	r := chi.NewRouter()

	// CORS — must be first, before all other middleware and routes.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)

	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fetcherStatus := "running"
		select {
		case <-docFetcher.Running():
			fetcherStatus = "stopped"
		default:
		}
		dispatcherStatus := "running"
		select {
		case <-webhookDispatcher.Running():
			dispatcherStatus = "stopped"
		default:
		}
		fmt.Fprintf(w, `{"status":"ok","env":"%s","workers":{"document_fetcher":"%s","webhook_dispatcher":"%s"}}`,
			cfg.App.Env, fetcherStatus, dispatcherStatus)
	})

	r.Group(func(pub chi.Router) {
		pub.Use(handler.RateLimiter(cfg.RateLimit.VerifyPerMinute))
		pub.Get("/verify/{signature_id}", verifyHandler.HandleVerify)
	})

	// Auth routes — public (no auth required).
	r.Post("/auth/register", authHandler.HandleRegister)
	r.Post("/auth/login", authHandler.HandleLogin)

	// Auth routes — require JWT.
	jwtMw := handler.JWTAuth(authSvc)
	r.Group(func(protected chi.Router) {
		protected.Use(jwtMw)
		protected.Get("/auth/me", authHandler.HandleMe)
		protected.Post("/auth/logout", authHandler.HandleLogout)
	})

	// Admin routes — require JWT + admin role.
	r.Group(func(admin chi.Router) {
		admin.Use(jwtMw)
		admin.Use(handler.RequireAdmin)
		admin.Get("/api/admin/tenants", adminHandler.HandleListTenants)
		admin.Post("/api/admin/tenants", adminHandler.HandleCreateTenant)
		admin.Get("/api/admin/tenants/{tenant_id}/keys", adminHandler.HandleListKeys)
		admin.Post("/api/admin/tenants/{tenant_id}/keys", adminHandler.HandleCreateKey)
		admin.Delete("/api/admin/tenants/{tenant_id}/keys/{key_id}", adminHandler.HandleDeactivateKey)
		admin.Get("/api/admin/users", adminHandler.HandleListUsers)
		admin.Post("/api/admin/users", adminHandler.HandleCreateUser)
		admin.Patch("/api/admin/users/{user_id}", adminHandler.HandleUpdateUser)
		admin.Delete("/api/admin/users/{user_id}", adminHandler.HandleDeleteUser)
	})

	// Demo routes — no auth, for frontend testing only.
	r.Post("/api/demo/upload", demoHandler.HandleUpload)
	r.Get("/api/demo/download/{id}", demoHandler.HandleDownload)

	// /api/v1 — supports both JWT and API-key auth.
	dualMw := handler.DualAuth(jwtMw, handler.APIKeyAuth(queries))
	r.Route("/api/v1", func(api chi.Router) {
		api.Use(dualMw)
		api.Use(handler.RateLimiter(cfg.RateLimit.APIPerMinute))

		api.Post("/documents", signHandler.HandleCreateDocument)
		api.Get("/documents/{id}", signHandler.HandleGetDocument)
		api.Post("/documents/{id}/sign", signHandler.HandleSign)
		api.Post("/documents/{id}/sign-async", signHandler.HandleSignAsync)
		api.Get("/documents/{id}/sign-status", signHandler.HandleSignStatus)

		// Production upload/download — paths avoid {id} wildcard conflict.
		api.Post("/upload", documentHandler.HandleUploadDocument)
		api.Get("/documents/{id}/file", documentHandler.HandleDownloadDocument)

		// Batch endpoints.
		api.Post("/batch/upload", batchHandler.HandleBatchUpload)
		api.Post("/batch/sign", batchHandler.HandleBatchSign)

		// Applications endpoints.
		api.Post("/applications/{external_id}/submit", appHandler.HandleSubmit)
		api.Get("/applications/{external_id}/status", appHandler.HandleStatus)
		api.Post("/applications/{external_id}/sign", appHandler.HandleSign)
		api.Post("/applications/{external_id}/cancel", appHandler.HandleCancel)
		api.Patch("/applications/{external_id}/refresh-urls", appHandler.HandleRefreshURLs)
		api.Post("/applications/{external_id}/retry-upload", appHandler.HandleRetryUpload)
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.App.Port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("API server starting on :%d (APP_ENV=%s)", cfg.App.Port, cfg.App.Env)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	cancelWorkers()
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutCancel()

	if err := srv.Shutdown(shutCtx); err != nil {
		log.Fatalf("Forced shutdown: %v", err)
	}
	log.Println("Server stopped")
}
