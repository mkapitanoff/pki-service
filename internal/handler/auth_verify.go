package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/reqctx"
)

// AuthVerifyHandler обслуживает верификацию ЭЦП при ВХОДЕ (не при подписании
// документа). Keycloak EDS-authenticator выдаёт пользователю одноразовый
// challenge, NCALayer подписывает его detached-подписью, а этот эндпоинт
// подтверждает: подпись валидна, сертификат не отозван, и вот чей это ИИН/БИН.
//
// Контракт: fin4b-platform/docs/contracts/chandra-auth-verify-signature.md
//
// Stateless по определению: ни записи в БД, ни S3, ни TSP, ни tenant-контекста
// документооборота. Signing-путь (sign_complete.go и пр.) не затрагивается —
// переиспользуется только существующая крипто-граница ncanode.VerifyCMS.
type AuthVerifyHandler struct {
	nc ncanode.NCANodeClient
}

func NewAuthVerifyHandler(nc ncanode.NCANodeClient) *AuthVerifyHandler {
	return &AuthVerifyHandler{nc: nc}
}

type authVerifyRequest struct {
	CMS       string `json:"cms"`
	Challenge string `json:"challenge"`
}

type authVerifyResponse struct {
	Valid         bool   `json:"valid"`
	IIN           string `json:"iin"`
	BIN           string `json:"bin"`
	CommonName    string `json:"common_name"`
	OrgName       string `json:"org_name"`
	SignerType    string `json:"signer_type"`
	CertSerial    string `json:"cert_serial"`
	CertNotBefore string `json:"cert_not_before"`
	CertNotAfter  string `json:"cert_not_after"`
	CAName        string `json:"ca_name"`
	OCSPStatus    string `json:"ocsp_status"`
}

// HandleVerifySignature — POST /api/v1/auth/verify-signature.
//
// Привязка к challenge обеспечивается на уровне параметра: NCANode сверяет
// messageDigest из подписи с дайджестом переданного содержимого, то есть подпись
// не пройдёт, если она сделана не над нашим challenge. Свежесть/одноразовость
// самого challenge — ответственность вызывающего (Keycloak authenticator
// привязывает его к authSession), здесь это сознательно не хранится.
func (h *AuthVerifyHandler) HandleVerifySignature(w http.ResponseWriter, r *http.Request) {
	var req authVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErrorReq(w, r, apperr.ErrInvalidRequest.WithCause(err))
		return
	}

	req.CMS = strings.TrimSpace(req.CMS)
	req.Challenge = strings.TrimSpace(req.Challenge)
	if req.CMS == "" || req.Challenge == "" {
		respondErrorReq(w, r, apperr.ErrInvalidRequest.WithDetails(map[string]any{
			"required": []string{"cms", "challenge"},
		}))
		return
	}

	// Detached-подпись: NCANode сам считает дайджест подписанного содержимого и
	// сверяет его с messageDigest из signed-attributes — это и связывает подпись
	// с конкретным выданным challenge.
	//
	// ВАЖНО (проверено на живом NCANode 2026-07-28, реальная ЭЦП ГОСТ-2022):
	// сюда НЕЛЬЗЯ передавать sha256(challenge). Казахстанская ЭЦП подписывает
	// ГОСТ-дайджестом (64 байта), sha256 (32 байта) с ним не совпадёт никогда.
	// NCANode ждёт в data САМО подписанное содержимое в base64 и хэширует его сам.
	//
	// Что именно подписано: страница входа передаёт в NCALayer
	// args.data = base64(challenge) при signingParams.decode = false, а значит
	// NCALayer подписывает ЭТОТ BASE64-ТЕКСТ как есть. Отсюда двойное кодирование
	// ниже — оно не описка. Если на клиенте когда-нибудь поставят decode = true,
	// подписан будет уже сам challenge, и здесь останется один слой base64;
	// менять обе стороны только вместе.
	signedContent := base64.StdEncoding.EncodeToString([]byte(req.Challenge))
	data := base64.StdEncoding.EncodeToString([]byte(signedContent))

	vr, err := h.nc.VerifyCMSWithRevocation(r.Context(), req.CMS, data)
	if err != nil {
		switch {
		case errors.Is(err, ncanode.ErrCMSInvalid):
			respondErrorReq(w, r, apperr.ErrCMSInvalid)
		case errors.Is(err, ncanode.ErrCertRevoked):
			respondErrorReq(w, r, apperr.ErrCertRevoked)
		default:
			respondErrorReq(w, r, apperr.ErrInternal.WithCause(err))
		}
		return
	}

	if !vr.Valid {
		respondErrorReq(w, r, apperr.ErrCMSInvalid)
		return
	}

	// OCSP обязателен для входа: пускаем только по заведомо действующему
	// сертификату. "unknown" — не пускаем: при аутентификации отсутствие
	// подтверждения статуса трактуем не в пользу входа.
	if vr.OCSPStatus != ncanode.OCSPStatusGood {
		respondErrorReq(w, r, apperr.ErrCertRevoked.WithDetails(map[string]any{
			"ocsp_status": vr.OCSPStatus,
		}))
		return
	}

	// PII: сам CMS и полный ИИН в логи не пишем.
	log.Printf("auth_verify.ok request_id=%s iin=%s signer_type=%s",
		reqctx.RequestID(r.Context()), pdf.MaskIIN(vr.SignerIIN), vr.SignerType)

	respondJSONReq(w, r, http.StatusOK, authVerifyResponse{
		Valid:         true,
		IIN:           vr.SignerIIN,
		BIN:           vr.SignerBIN,
		CommonName:    vr.SignerName,
		OrgName:       vr.OrgName,
		SignerType:    vr.SignerType,
		CertSerial:    vr.CertSerial,
		CertNotBefore: vr.CertNotBefore.UTC().Format("2006-01-02T15:04:05Z"),
		CertNotAfter:  vr.CertNotAfter.UTC().Format("2006-01-02T15:04:05Z"),
		CAName:        vr.CAName,
		OCSPStatus:    vr.OCSPStatus,
	})
}
