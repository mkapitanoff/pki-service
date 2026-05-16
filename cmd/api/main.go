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

	"github.com/mkapitanoff/pki-service/internal/config"
	"github.com/mkapitanoff/pki-service/internal/handler"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/service"
	"github.com/mkapitanoff/pki-service/internal/storage"
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

	signSvc := service.NewSignService(db, ncClient, store, queries, nil, cfg.App.VerifyBaseURL)
	signHandler := handler.NewSignHandler(signSvc, queries)
	verifyHandler := handler.NewVerifyHandler(queries)
	demoHandler := handler.NewDemoHandler(queries, store)
	documentHandler := handler.NewDocumentHandler(queries, store, cfg.App.VerifyBaseURL)

	r := chi.NewRouter()

	// CORS — allow the demo frontend at localhost:3000.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "http://localhost:3000")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
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
		fmt.Fprintf(w, `{"status":"ok","env":"%s"}`, cfg.App.Env)
	})

	r.Group(func(pub chi.Router) {
		pub.Use(handler.RateLimiter(cfg.RateLimit.VerifyPerMinute))
		pub.Get("/verify/{signature_id}", verifyHandler.HandleVerify)
	})

	// Demo routes — no auth, for frontend testing only.
	r.Post("/api/demo/upload", demoHandler.HandleUpload)
	r.Get("/api/demo/download/{id}", demoHandler.HandleDownload)

	r.Route("/api/v1", func(api chi.Router) {
		api.Use(handler.APIKeyAuth(queries))
		api.Use(handler.RateLimiter(cfg.RateLimit.APIPerMinute))

		// Document management
		api.Post("/documents", signHandler.HandleCreateDocument)
		api.Get("/documents/{id}", signHandler.HandleGetDocument)
		api.Post("/documents/{id}/sign", signHandler.HandleSign)

		// Production upload/download — paths avoid {id} wildcard conflict
		api.Post("/upload", documentHandler.HandleUploadDocument)
		api.Get("/documents/{id}/file", documentHandler.HandleDownloadDocument)
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Forced shutdown: %v", err)
	}
	log.Println("Server stopped")
}
