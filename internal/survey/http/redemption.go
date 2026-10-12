package http

import (
	"io"
	"net/http"
	"strings"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/xuri/excelize/v2"
)

func (h *Handler) redemptionCodes(w http.ResponseWriter, r *http.Request, id surveyport.ID, tail []string) {
	service, ok := h.definitions.(surveyport.RedemptionAdministration)
	if !ok {
		writeError(w, 503, "survey_unavailable")
		return
	}
	if len(tail) == 0 && r.Method == http.MethodGet {
		if _, ok := h.exportPrincipal(w, r); !ok {
			return
		}
		limit, offset, ok := pageParams(r, 200)
		if !ok {
			writeError(w, 400, "invalid_request")
			return
		}
		result, e := service.ListRedemptionCodes(r.Context(), id, limit, offset)
		if e != nil {
			resultError(w, e)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, 200, result)
		return
	}
	if len(tail) == 1 && tail[0] == "template" && r.Method == http.MethodGet {
		if _, ok := h.exportPrincipal(w, r); !ok {
			return
		}
		if _, e := h.definitions.Get(r.Context(), id); e != nil {
			resultError(w, e)
			return
		}
		f := excelize.NewFile()
		defer f.Close()
		_ = f.SetCellStr("Sheet1", "A1", "兑换码")
		style, e := f.NewStyle(&excelize.Style{NumFmt: 49})
		if e == nil {
			e = f.SetColStyle("Sheet1", "A", style)
		}
		if e != nil {
			writeError(w, 503, "survey_unavailable")
			return
		}
		data, e := f.WriteToBuffer()
		if e != nil {
			writeError(w, 503, "survey_unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		w.Header().Set("Content-Disposition", `attachment; filename="redemption-codes.xlsx"`)
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data.Bytes())
		return
	}
	if len(tail) == 1 && tail[0] == "imports" && r.Method == http.MethodPost {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody+(1<<20))
		if e := r.ParseMultipartForm(maxBody); e != nil {
			writeJSON(w, 400, map[string]any{"code": "redemption_import_invalid", "message": "请选择 .xlsx 文件，文件不能超过现有 2MB 导入预算"})
			return
		}
		defer r.MultipartForm.RemoveAll()
		if len(r.MultipartForm.Value) != 0 || len(r.MultipartForm.File) != 1 || len(r.MultipartForm.File["file"]) != 1 {
			writeError(w, 400, "invalid_request")
			return
		}
		header := r.MultipartForm.File["file"][0]
		if !strings.HasSuffix(strings.ToLower(header.Filename), ".xlsx") {
			writeJSON(w, 400, map[string]any{"code": "redemption_import_invalid", "message": "请使用 .xlsx 兑换码模板"})
			return
		}
		file, e := header.Open()
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		defer file.Close()
		data, e := io.ReadAll(io.LimitReader(file, maxBody+1))
		if e != nil || len(data) > maxBody {
			writeError(w, 400, "invalid_request")
			return
		}
		result, e := service.ImportRedemptionCodes(r.Context(), id, actor.InternalID, idempotency(r), data)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	writeError(w, 404, "not_found")
}
