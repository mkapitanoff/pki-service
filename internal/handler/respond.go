package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
)

func toNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func respondError(w http.ResponseWriter, err error) {
	ae := apperr.As(err)
	if ae == nil {
		ae = apperr.ErrInternal
	}
	respondJSON(w, ae.Status, map[string]any{
		"error": map[string]any{
			"code":    ae.Code,
			"message": ae.Message,
		},
	})
}
