// Seed creates the first admin user in the database.
// Usage: APP_ENV=prod go run cmd/seed/main.go
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/lib/pq"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"github.com/mkapitanoff/pki-service/internal/config"
	"github.com/mkapitanoff/pki-service/internal/repository"
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
	if err := db.Ping(); err != nil {
		log.Fatalf("db ping: %v", err)
	}

	queries := repository.New(db)
	ctx := context.Background()

	const (
		adminEmail    = "admin@pki.local"
		adminPassword = "Admin1234!"
		adminName     = "PKI Admin"
	)
	defaultTenantID := uuid.MustParse("8ba64263-e516-4574-a7e8-fadad9663eea")

	// Check if admin already exists.
	existing, err := queries.GetUserByEmail(ctx, adminEmail)
	if err == nil {
		fmt.Printf("Admin already exists: id=%s email=%s role=%s\n",
			existing.ID, existing.Email, existing.Role)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(adminPassword), 12)
	if err != nil {
		log.Fatalf("bcrypt: %v", err)
	}

	user, err := queries.CreateUser(ctx, repository.CreateUserParams{
		Email:        adminEmail,
		PasswordHash: string(hash),
		Name:         adminName,
		TenantID:     uuid.NullUUID{UUID: defaultTenantID, Valid: true},
		Role:         "admin",
	})
	if err != nil {
		log.Fatalf("create user: %v", err)
	}

	fmt.Printf("Admin created:\n  id:    %s\n  email: %s\n  role:  %s\n  tenant: %s\n",
		user.ID, user.Email, user.Role, user.TenantID.UUID)
}
