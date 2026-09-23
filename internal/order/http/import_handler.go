package http

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
)

const maxImportBody = 2 << 20

type OrderOnlyImporter interface {
	Apply(context.Context, ordermigration.Manifest) (ordermigration.Result, error)
}

type OrderOnlyReconciler interface {
	ReconcileOrders(context.Context, ordermigration.Manifest) (ordermigration.OrderOnlyReconciliation, error)
}

type ImportHandler struct {
	importer   OrderOnlyImporter
	reconciler OrderOnlyReconciler
	security   RequestSecurity
}

func NewImportHandler(importer OrderOnlyImporter, reconciler OrderOnlyReconciler, security RequestSecurity) (*ImportHandler, error) {
	if importer == nil || reconciler == nil || security == nil {
		return nil, errors.New("order import HTTP dependencies are required")
	}
	return &ImportHandler{importer: importer, reconciler: reconciler, security: security}, nil
}

func (h *ImportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !orderImportWriteRole(principal) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, http.StatusForbidden, "csrf_required")
		return
	}
	manifest, ok := decodeOrderOnlyManifest(w, r)
	if !ok {
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch path {
	case "/api/admin/order-imports/inspect":
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "inspect", "run_key": manifest.RunKey, "manifest_sha256": hex.EncodeToString(manifest.Digest[:]), "summary": manifest.Summary()})
	case "/api/admin/order-imports/apply":
		if strings.TrimSpace(r.Header.Get("Idempotency-Key")) != manifest.RunKey || r.Header.Get("X-Confirm-Apply") != manifest.RunKey {
			writeError(w, http.StatusConflict, "apply_confirmation_mismatch")
			return
		}
		result, applyErr := h.importer.Apply(r.Context(), manifest)
		if applyErr != nil {
			importResultError(w, applyErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "apply", "run_key": manifest.RunKey, "result": result})
	case "/api/admin/order-imports/reconcile":
		if strings.TrimSpace(r.Header.Get("Idempotency-Key")) != manifest.RunKey {
			writeError(w, http.StatusConflict, "reconciliation_confirmation_mismatch")
			return
		}
		result, reconcileErr := h.reconciler.ReconcileOrders(r.Context(), manifest)
		if reconcileErr != nil {
			importResultError(w, reconcileErr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": "reconcile", "run_key": manifest.RunKey, "result": result})
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func orderImportWriteRole(principal accessdomain.Principal) bool {
	if principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func decodeOrderOnlyManifest(w http.ResponseWriter, r *http.Request) (ordermigration.Manifest, bool) {
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "application_json_required")
		return ordermigration.Manifest{}, false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxImportBody+1))
	if err != nil || len(raw) == 0 || len(raw) > maxImportBody {
		writeError(w, http.StatusRequestEntityTooLarge, "snapshot_too_large")
		return ordermigration.Manifest{}, false
	}
	manifest, err := ordermigration.Parse(raw)
	if err != nil || ordermigration.ValidateOrderOnly(manifest) != nil {
		writeError(w, http.StatusBadRequest, "invalid_order_only_snapshot")
		return ordermigration.Manifest{}, false
	}
	provided, err := hex.DecodeString(strings.TrimSpace(r.Header.Get("X-Manifest-SHA256")))
	if err != nil || len(provided) != 32 {
		writeError(w, http.StatusConflict, "manifest_digest_mismatch")
		return ordermigration.Manifest{}, false
	}
	var digest [32]byte
	copy(digest[:], provided)
	if !ordermigration.DigestMatches(manifest, digest) {
		writeError(w, http.StatusConflict, "manifest_digest_mismatch")
		return ordermigration.Manifest{}, false
	}
	return manifest, true
}

func importResultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ordermigration.ErrInvalidManifest), errors.Is(err, ordermigration.ErrNotOrderOnly):
		writeError(w, http.StatusBadRequest, "invalid_order_only_snapshot")
	case errors.Is(err, ordermigration.ErrRunConflict):
		writeError(w, http.StatusConflict, "import_run_conflict")
	default:
		writeError(w, http.StatusServiceUnavailable, "order_import_unavailable")
	}
}
