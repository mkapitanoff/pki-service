package worker

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
)

// VerificationConfig — параметры verification-воркера.
type VerificationConfig struct {
	TickInterval     time.Duration // период тика (default 5s)
	BatchSize        int32         // макс. кол-во документов за тик (default 50)
	Deadline         time.Duration // окно с момента signed_at, после — финал unavailable (default 24h)
	BackoffSchedule  []time.Duration
}

// VerificationWorker делает фоновую сверку x-amz-meta-sha256 ↔ content_hash
// для подписанных документов и пишет результат во внутренние поля
// signing_session_documents.verification_*. Наружу ничего не отдаётся — при
// mismatch / unavailable пишется запись в audit_log + WARN-лог.
type VerificationWorker struct {
	cfg     VerificationConfig
	queries *repository.Queries
	source  s3client.MetaHashFetcher
	running chan struct{}
}

func NewVerificationWorker(
	cfg VerificationConfig,
	queries *repository.Queries,
	source s3client.MetaHashFetcher,
) *VerificationWorker {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 5 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.Deadline <= 0 {
		cfg.Deadline = 24 * time.Hour
	}
	if len(cfg.BackoffSchedule) == 0 {
		cfg.BackoffSchedule = []time.Duration{
			60 * time.Second,
			5 * time.Minute,
			30 * time.Minute,
			2 * time.Hour,
		}
	}
	return &VerificationWorker{
		cfg:     cfg,
		queries: queries,
		source:  source,
		running: make(chan struct{}),
	}
}

func (w *VerificationWorker) Running() <-chan struct{} { return w.running }

func (w *VerificationWorker) Run(ctx context.Context) {
	defer close(w.running)
	log.Println("verification_worker: started")
	t := time.NewTicker(w.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("verification_worker: stopped")
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *VerificationWorker) tick(ctx context.Context) {
	docs, err := w.queries.ListDocumentsForVerification(ctx, int32(w.cfg.BatchSize))
	if err != nil {
		log.Printf("verification_worker: list: %v", err)
		return
	}
	for _, doc := range docs {
		w.process(ctx, doc)
	}
}

// terminalStatus — финальные значения, при которых воркер больше не пробует.
func isTerminalStatus(s string) bool {
	return s == "verified" || s == "mismatch" || s == "unavailable"
}

func (w *VerificationWorker) process(ctx context.Context, doc repository.SigningSessionDocument) {
	// Bucket/key могут быть NULL у legacy-документов (computed-mode без
	// присланных полей). Тогда сразу финалим как unavailable — у воркера
	// нет адреса для HEAD.
	if !doc.SourceS3Bucket.Valid || doc.SourceS3Bucket.String == "" ||
		!doc.SourceS3Key.Valid || doc.SourceS3Key.String == "" {
		w.finalize(ctx, doc, "unavailable", "missing source bucket/key", "")
		return
	}
	if !doc.ContentHash.Valid || doc.ContentHash.String == "" {
		w.finalize(ctx, doc, "unavailable", "missing content_hash", "")
		return
	}

	headCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	head, err := w.source.HeadObject(headCtx, doc.SourceS3Bucket.String, doc.SourceS3Key.String)
	if err != nil {
		w.handleError(ctx, doc, fmt.Errorf("head: %w", err))
		return
	}

	rawMeta := pickSha256(head.Metadata)
	if rawMeta == "" {
		w.handleError(ctx, doc, errors.New("no x-amz-meta-sha256 in head response"))
		return
	}
	metaHex, err := normalizeMetaHash(rawMeta)
	if err != nil {
		w.finalize(ctx, doc, "mismatch", fmt.Sprintf("invalid meta hash: %v", err), "")
		return
	}

	if !strings.EqualFold(metaHex, doc.ContentHash.String) {
		w.finalize(ctx, doc, "mismatch",
			fmt.Sprintf("meta_hash != content_hash (meta=%s)", metaHex), metaHex)
		return
	}
	w.finalize(ctx, doc, "verified", "", metaHex)
}

// handleError — классификация ошибки HEAD: либо retry с backoff, либо финал unavailable.
func (w *VerificationWorker) handleError(ctx context.Context, doc repository.SigningSessionDocument, cause error) {
	// Проверим дедлайн: signed_at + Deadline. Если он истёк — финал.
	if doc.SignedAt.Valid && time.Since(doc.SignedAt.Time) > w.cfg.Deadline {
		w.finalize(ctx, doc, "unavailable", cause.Error(), "")
		return
	}
	// Иначе — retry с backoff по количеству попыток.
	idx := int(doc.VerificationAttempts)
	if idx >= len(w.cfg.BackoffSchedule) {
		idx = len(w.cfg.BackoffSchedule) - 1
	}
	nextAt := time.Now().Add(w.cfg.BackoffSchedule[idx])
	if err := w.queries.UpdateDocumentVerification(ctx, repository.UpdateDocumentVerificationParams{
		ID:                 doc.ID,
		VerificationStatus: sql.NullString{String: "retrying", Valid: true},
		VerificationError:  sql.NullString{String: cause.Error(), Valid: true},
		VerificationNextAt: sql.NullTime{Time: nextAt, Valid: true},
		SourceMetaHash:     sql.NullString{},
	}); err != nil {
		log.Printf("verification_worker: update retry doc=%s: %v", doc.ID, err)
		return
	}
	log.Printf("verification_worker: retry doc=%s in %s (attempt %d): %v",
		doc.ID, w.cfg.BackoffSchedule[idx], doc.VerificationAttempts+1, cause)
}

// finalize пишет конечный verification_status, при не-verified — audit_log + WARN.
func (w *VerificationWorker) finalize(
	ctx context.Context,
	doc repository.SigningSessionDocument,
	status, errMsg, metaHashHex string,
) {
	if err := w.queries.UpdateDocumentVerification(ctx, repository.UpdateDocumentVerificationParams{
		ID:                 doc.ID,
		VerificationStatus: sql.NullString{String: status, Valid: true},
		VerificationError:  sql.NullString{String: errMsg, Valid: errMsg != ""},
		VerificationNextAt: sql.NullTime{},
		SourceMetaHash:     sql.NullString{String: metaHashHex, Valid: metaHashHex != ""},
	}); err != nil {
		log.Printf("verification_worker: finalize update doc=%s: %v", doc.ID, err)
		return
	}
	if err := w.queries.RecalcSessionVerification(ctx, doc.SessionID); err != nil {
		log.Printf("verification_worker: recalc session=%s: %v", doc.SessionID, err)
	}
	if status != "verified" {
		log.Printf("verification_worker: WARN doc=%s status=%s err=%s", doc.ID, status, errMsg)
		w.writeAudit(ctx, doc, status, errMsg, metaHashHex)
	}
}

func (w *VerificationWorker) writeAudit(
	ctx context.Context,
	doc repository.SigningSessionDocument,
	status, errMsg, metaHashHex string,
) {
	meta := map[string]any{
		"status":             status,
		"error":              errMsg,
		"attempts":           doc.VerificationAttempts + 1,
		"content_hash":       nullStringOrEmpty(doc.ContentHash),
		"source_meta_hash":   metaHashHex,
		"source_s3_bucket":   nullStringOrEmpty(doc.SourceS3Bucket),
		"source_s3_key":      nullStringOrEmpty(doc.SourceS3Key),
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		log.Printf("verification_worker: marshal audit meta: %v", err)
		return
	}
	// tenant_id берём через сессию — она хранится в signing_sessions; но
	// чтобы не делать лишний select на каждый mismatch, оставляем 00000…
	// Audit-row всё равно достаточно для расследования по entity_id.
	tenantID := uuid.Nil
	if err := w.queries.CreateAuditLog(ctx, repository.CreateAuditLogParams{
		TenantID:   tenantID,
		Action:     "verification.failed",
		EntityType: "signing_session_document",
		EntityID:   uuid.NullUUID{UUID: doc.ID, Valid: true},
		Meta:       pqtype.NullRawMessage{RawMessage: raw, Valid: true},
	}); err != nil {
		log.Printf("verification_worker: write audit doc=%s: %v", doc.ID, err)
	}
}

// pickSha256 ищет хэш в любом из распространённых имён.
func pickSha256(meta map[string]string) string {
	for _, k := range []string{"sha256", "x-amz-meta-sha256", "amz-meta-sha256"} {
		if v, ok := meta[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// normalizeMetaHash принимает base64 или hex (по длине) и возвращает hex.
func normalizeMetaHash(v string) (string, error) {
	v = strings.TrimSpace(v)
	// 64 hex символа?
	if len(v) == 64 {
		if _, err := hex.DecodeString(v); err == nil {
			return strings.ToLower(v), nil
		}
	}
	// base64?
	var (
		raw []byte
		err error
	)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		raw, err = enc.DecodeString(v)
		if err == nil {
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("not base64 or hex")
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("want 32 bytes, got %d", len(raw))
	}
	return hex.EncodeToString(raw), nil
}

func nullStringOrEmpty(n sql.NullString) string {
	if !n.Valid {
		return ""
	}
	return n.String
}
