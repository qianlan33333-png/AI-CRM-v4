package http

import (
	"encoding/json"
	"errors"
	configapp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	configport "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	"io"
	"net/http"
)

func (h *Handler) WithAIModels(service configport.AIModelSettings) { h.models = service }
func (h *Handler) aiModel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if h.models == nil {
		writeError(w, 503, "model_settings_unavailable")
		return
	}
	if r.Method == http.MethodGet {
		if _, ok := h.read(w, r); !ok {
			return
		}
		v, e := h.models.ReadAIModel(r.Context())
		if e != nil {
			writeError(w, 503, "model_settings_unavailable")
			return
		}
		writeJSON(w, 200, v)
		return
	}
	if r.Method != http.MethodPut {
		writeError(w, 405, "method_not_allowed")
		return
	}
	p, ok := h.mutate(w, r)
	if !ok {
		return
	}
	var command configport.AIModelCommand
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&command) != nil {
		writeError(w, 400, "invalid_model_settings")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, 400, "invalid_model_settings")
		return
	}
	command.ActorID = p.InternalID
	v, e := h.models.SaveAIModel(r.Context(), command)
	switch {
	case errors.Is(e, configapp.ErrAIModelConflict):
		writeError(w, 409, "model_settings_conflict")
	case errors.Is(e, configapp.ErrAIModelInvalid):
		writeError(w, 400, "invalid_model_settings")
	case e != nil:
		writeError(w, 503, "model_settings_unavailable")
	default:
		writeJSON(w, 200, v)
	}
}
