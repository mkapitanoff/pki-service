package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	stderrors "errors"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

// Claims is the parsed JWT payload returned to callers.
type Claims struct {
	UserID   uuid.UUID
	Email    string
	Role     string
	TenantID uuid.UUID
}

// jwtClaims is the internal JWT payload structure.
type jwtClaims struct {
	Email    string    `json:"email"`
	Role     string    `json:"role"`
	TenantID uuid.UUID `json:"tenant_id"`
	jwt.RegisteredClaims
}

// AuthService handles user registration, login, and token validation.
type AuthService struct {
	queries   *repository.Queries
	jwtSecret []byte
}

func NewAuthService(queries *repository.Queries, jwtSecret string) *AuthService {
	return &AuthService{
		queries:   queries,
		jwtSecret: []byte(jwtSecret),
	}
}

// Register creates a new user with role "user". Returns 409 if email is taken.
// If tenantID is uuid.Nil, a personal tenant is automatically created.
func (s *AuthService) Register(ctx context.Context, email, password, name string, tenantID uuid.UUID) (*repository.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	if tenantID == uuid.Nil {
		tenant, err := s.queries.CreateTenant(ctx, repository.CreateTenantParams{
			Name: name + " Personal",
			Type: repository.TenantTypeIndividual,
		})
		if err != nil {
			return nil, apperr.ErrInternal.WithCause(err)
		}
		tenantID = tenant.ID
	}

	user, err := s.queries.CreateUser(ctx, repository.CreateUserParams{
		Email:        email,
		PasswordHash: string(hash),
		Name:         name,
		TenantID:     uuid.NullUUID{UUID: tenantID, Valid: true},
		Role:         "user",
	})
	if err != nil {
		var pqErr *pq.Error
		if stderrors.As(err, &pqErr) && pqErr.Code == "23505" {
			return nil, apperr.ErrEmailTaken
		}
		return nil, apperr.ErrInternal.WithCause(err)
	}

	return &user, nil
}

// Login verifies credentials and returns a signed JWT.
// Also stores token hash in auth_tokens for revocation support.
func (s *AuthService) Login(ctx context.Context, email, password string) (string, error) {
	user, err := s.queries.GetUserByEmail(ctx, email)
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return "", apperr.ErrInvalidCredentials
		}
		return "", apperr.ErrInternal.WithCause(err)
	}

	if !user.IsActive.Bool {
		return "", apperr.ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return "", apperr.ErrInvalidCredentials
	}

	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	claims := jwtClaims{
		Email:    user.Email,
		Role:     user.Role,
		TenantID: user.TenantID.UUID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.jwtSecret)
	if err != nil {
		return "", apperr.ErrInternal.WithCause(err)
	}

	// Store token hash for revocation.
	h := sha256.Sum256([]byte(signed))
	tokenHash := hex.EncodeToString(h[:])
	_, storeErr := s.queries.CreateAuthToken(ctx, repository.CreateAuthTokenParams{
		UserID:    uuid.NullUUID{UUID: user.ID, Valid: true},
		TokenHash: tokenHash,
		ExpiresAt: expiresAt,
	})
	if storeErr != nil {
		fmt.Printf("[auth] warn: store token hash: %v\n", storeErr)
	}

	// Best-effort: update last login timestamp.
	_ = s.queries.UpdateUserLastLogin(ctx, user.ID)

	return signed, nil
}

// ValidateToken parses and verifies a JWT string. Returns Claims on success.
func (s *AuthService) ValidateToken(_ context.Context, tokenString string) (*Claims, error) {
	var jc jwtClaims
	token, err := jwt.ParseWithClaims(tokenString, &jc, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return nil, apperr.ErrUnauthorized
	}

	userID, err := uuid.Parse(jc.Subject)
	if err != nil {
		return nil, apperr.ErrUnauthorized
	}

	return &Claims{
		UserID:   userID,
		Email:    jc.Email,
		Role:     jc.Role,
		TenantID: jc.TenantID,
	}, nil
}

// Me returns the user by ID.
func (s *AuthService) Me(ctx context.Context, userID uuid.UUID) (*repository.User, error) {
	user, err := s.queries.GetUserByID(ctx, userID)
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apperr.ErrUnauthorized
		}
		return nil, apperr.ErrInternal.WithCause(err)
	}
	return &user, nil
}

// RevokeToken deletes the token hash from auth_tokens (logout).
func (s *AuthService) RevokeToken(ctx context.Context, tokenString string) {
	h := sha256.Sum256([]byte(tokenString))
	tokenHash := hex.EncodeToString(h[:])
	_ = s.queries.DeleteAuthToken(ctx, tokenHash)
}
