package main

import (
	"context"
	"database/sql"
	"encoding/json"
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

	// Ensure signing-related buckets exist (creates them if absent, no-op otherwise).
	signingCfg := cfg.Signing
	signingBuckets := []string{signingCfg.CacheBucket, signingCfg.SignedBucket, signingCfg.CMSBucket}
	if signingCfg.CacheBucket == "" {
		signingBuckets = []string{"pki-cache", "pki-signed", "pki-cms"}
	}
	if err := store.EnsureBuckets(context.Background(), signingBuckets...); err != nil {
		log.Fatalf("storage: ensure signing buckets: %v", err)
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
	webhookHandler := handler.NewWebhookHandler(queries)

	extS3 := s3client.NewHTTPExternalS3Client()
	appHandler := handler.NewApplicationHandler(queries, signSvc, extS3, store)
	signInitiateHandler := handler.NewSignInitiateHandler(queries, extS3, store, cfg.Verification.AllowedBuckets)
	verInitialDelay := time.Duration(cfg.Verification.InitialDelaySec) * time.Second
	if verInitialDelay <= 0 {
		verInitialDelay = 60 * time.Second
	}
	signCompleteHandler := handler.NewSignCompleteHandler(
		queries, ncClient, store, extS3,
		cfg.Verification.Enabled, verInitialDelay,
		cfg.App.VerifyBaseURL,
	)
	signStatusHandler := handler.NewSignStatusHandler(queries, extS3, store)

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
	cleanupInterval := time.Duration(signingCfg.CleanupIntervalSec) * time.Second
	if cleanupInterval <= 0 {
		cleanupInterval = 10 * time.Minute
	}
	cacheTTL := time.Duration(signingCfg.CacheTTLSec) * time.Second
	if cacheTTL <= 0 {
		cacheTTL = 24 * time.Hour
	}
	sessionCleanup := worker.NewSessionCleanupWorker(worker.SessionCleanupConfig{
		CleanupInterval: cleanupInterval,
		CacheTTL:        cacheTTL,
	}, queries, store)
	deliveryPoller := worker.NewWebhookDeliveryPoller(worker.WebhookDeliveryPollerConfig{
		Interval:  5 * time.Second,
		BatchSize: 50,
	}, queries, &http.Client{Timeout: 15 * time.Second})
	go docFetcher.Run(workerCtx)
	go webhookDispatcher.Run(workerCtx)
	go sessionCleanup.Run(workerCtx)
	go deliveryPoller.Run(workerCtx)

	// Verification worker — внутренняя async-сверка x-amz-meta-sha256 ↔ content_hash.
	// Source-S3 клиент: если source_* пуст в storage-конфиге, используем
	// основной S3-клиент (предполагая один бакет/одну IAM-роль).
	var sourceClient s3client.MetaHashFetcher
	if cfg.Storage.SourceEndpoint != "" || cfg.Storage.SourceAccessKey != "" {
		sc, err := s3client.NewSourceS3Client(s3client.SourceS3Config{
			Endpoint:     cfg.Storage.SourceEndpoint,
			Region:       cfg.Storage.SourceRegion,
			AccessKey:    cfg.Storage.SourceAccessKey,
			SecretKey:    cfg.Storage.SourceSecretKey,
			UsePathStyle: cfg.Storage.SourceUsePathStyle,
		})
		if err != nil {
			log.Fatalf("source s3 client: %v", err)
		}
		sourceClient = sc
	} else {
		sourceClient = store
	}
	verWorker := worker.NewVerificationWorker(worker.VerificationConfig{
		TickInterval: time.Duration(cfg.Verification.TickIntervalSec) * time.Second,
		BatchSize:    int32(cfg.Verification.BatchSize),
		Deadline:     time.Duration(cfg.Verification.DeadlineHours) * time.Hour,
	}, queries, sourceClient)
	if cfg.Verification.Enabled {
		go verWorker.Run(workerCtx)
		log.Println("verification_worker: enabled")
	} else {
		log.Println("verification_worker: disabled (PKI_VERIFICATION_ENABLED=false)")
	}

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
			// X-Request-Id и Idempotency-Key — клиент (Lovable) шлёт их для
			// трейсинга и идемпотентности; без них в allow-headers браузерный
			// preflight падает → "Failed to fetch". Expose-Headers — чтобы JS
			// мог прочитать X-Request-Id из ответа для корреляции с логами.
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-Id, Idempotency-Key")
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-Id")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	})

	// RequestID должен быть ПЕРВЫМ, чтобы X-Request-Id попал и в логи
	// middleware.Logger, и в JSON-ответы при panic'ах.
	r.Use(handler.RequestIDMiddleware)
	r.Use(middleware.Logger)
	// JSONRecover вместо chi middleware.Recoverer — отдаёт JSON 500
	// вместо текста, чтобы Lovable Edge не парсил HTML на нашей стороне.
	r.Use(handler.JSONRecover)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		workerStatus := func(ch <-chan struct{}) string {
			select {
			case <-ch:
				return "stopped"
			default:
				return "running"
			}
		}

		// Bucket ping with short timeout — non-fatal if slow.
		pingCtx, pingCancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer pingCancel()
		bucketStatus := make(map[string]string, len(signingBuckets))
		for _, b := range signingBuckets {
			if err := store.PingBucket(pingCtx, b); err != nil {
				bucketStatus[b] = "unavailable"
			} else {
				bucketStatus[b] = "ok"
			}
		}

		resp := map[string]any{
			"status": "ok",
			"env":    cfg.App.Env,
			"workers": map[string]string{
				"document_fetcher":        workerStatus(docFetcher.Running()),
				"webhook_dispatcher":      workerStatus(webhookDispatcher.Running()),
				"session_cleanup":         workerStatus(sessionCleanup.Running()),
				"webhook_delivery_poller": workerStatus(deliveryPoller.Running()),
			},
			"storage": bucketStatus,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("health: encode response: %v", err)
		}
	})

	r.Group(func(pub chi.Router) {
		pub.Use(handler.RateLimiter(cfg.RateLimit.VerifyPerMinute))
		pub.Get("/verify/{signature_id}", verifyHandler.HandleVerify)
	})

	// API docs — public, без auth, без rate-limit.
	r.Get("/api/docs", func(w http.ResponseWriter, req *http.Request) {
		http.ServeFile(w, req, "docs/swagger.html")
	})
	r.Get("/api/docs/openapi.yaml", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		http.ServeFile(w, req, "docs/openapi.yaml")
	})
	// Сервис-агностичный контракт интеграции — публичный, отдаём как markdown.
	r.Get("/api/docs/integration.md", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		http.ServeFile(w, req, "docs/integration.md")
	})
	// Обратная совместимость: старый Lovable-специфичный путь → редирект на генерик.
	r.Get("/api/docs/integration-lovable.md", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/api/docs/integration.md", http.StatusMovedPermanently)
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
	registryHandler := handler.NewRegistryHandler(queries, store)
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
		admin.Get("/api/admin/signing-documents", registryHandler.HandleListRegistry)
		admin.Get("/api/admin/signing-documents/{id}/file", registryHandler.HandleDownloadRegistryDocument)
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
		// Лимит body 50 MiB на multipart-upload PDF.
		const uploadBodyLimit = 50 << 20  // 50 MiB
		const jsonBodyLimit   = 1 << 20   // 1 MiB

		api.Group(func(up chi.Router) {
			up.Use(handler.MaxBytes(uploadBodyLimit))
			up.Post("/upload", documentHandler.HandleUploadDocument)
		})
		api.Get("/documents/{id}/file", documentHandler.HandleDownloadDocument)

		// Batch endpoints — multipart с несколькими PDF.
		api.Group(func(b chi.Router) {
			b.Use(handler.MaxBytes(uploadBodyLimit))
			b.Post("/batch/upload", batchHandler.HandleBatchUpload)
		})
		api.Post("/batch/sign", batchHandler.HandleBatchSign)

		// Signing session endpoints — чисто JSON, лимит 1 MiB.
		api.Group(func(s chi.Router) {
			s.Use(handler.MaxBytes(jsonBodyLimit))
			s.Post("/sign/initiate", signInitiateHandler.HandleInitiate)
			s.Post("/sign/complete", signCompleteHandler.HandleComplete)
			s.Patch("/sign/refresh-urls", signStatusHandler.HandleRefreshURLs)
		})
		api.Get("/sign/status/{session_id}", signStatusHandler.HandleGetStatus)

		// Webhook subscription management.
		api.Get("/webhooks", webhookHandler.HandleList)
		api.Post("/webhooks", webhookHandler.HandleCreate)
		api.Delete("/webhooks/{webhook_id}", webhookHandler.HandleDelete)
		api.Post("/webhooks/{webhook_id}/test", webhookHandler.HandleTest)

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
