package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/xuri/excelize/v2"
)

type RedemptionStore interface {
	ListRedemptionCodes(context.Context, surveyport.ID, int32, int32) (surveyport.RedemptionPage, error)
	InsertRedemptionCode(context.Context, surveyport.ID, string, int64, time.Time) (bool, error)
	ConsumeRedemptionCode(context.Context, surveyport.ID, surveyport.ID, string, surveyport.ID) error
}

func importError(row int, reason string) error {
	if row > 0 {
		reason = fmt.Sprintf("第 %d 行：%s", row, reason)
	}
	return &surveyport.FieldError{Code: "redemption_import_invalid", Message: reason}
}

// The XLSX compressed request shares the existing 2MiB Survey input budget.
// Bound decompression to 64MiB on the 2GiB release host; no row-count quota.
func ParseRedemptionWorkbook(ctx context.Context, content []byte) ([]string, surveyport.RedemptionImportResult, error) {
	result := surveyport.RedemptionImportResult{}
	f, err := excelize.OpenReader(bytes.NewReader(content), excelize.Options{UnzipSizeLimit: 64 << 20, UnzipXMLSizeLimit: 2 << 20})
	if err != nil {
		return nil, result, importError(0, "Excel 文件损坏或无法读取，请使用 .xlsx 模板")
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, result, importError(0, "Excel 没有工作表")
	}
	sheet := sheets[0]
	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, result, importError(0, "无法读取首张工作表")
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, result, importError(1, "请使用包含“兑换码”列的模板")
	}
	header, err := rows.Columns()
	if err != nil {
		return nil, result, importError(1, "表头无法读取")
	}
	column := -1
	for i, v := range header {
		if strings.TrimSpace(v) == "兑换码" {
			if column >= 0 {
				return nil, result, importError(1, "只能有一列“兑换码”")
			}
			column = i
		}
	}
	if column < 0 {
		return nil, result, importError(1, "缺少“兑换码”列")
	}
	codes := []string{}
	seen := map[string]bool{}
	row := 1
	for rows.Next() {
		row++
		if err = ctx.Err(); err != nil {
			return nil, result, surveyport.ErrUnavailable
		}
		cell, _ := excelize.CoordinatesToCellName(column+1, row)
		formula, e := f.GetCellFormula(sheet, cell)
		if e != nil || formula != "" {
			return nil, result, importError(row, "兑换码不能使用公式，请粘贴为文本")
		}
		kind, e := f.GetCellType(sheet, cell)
		if e != nil {
			return nil, result, importError(row, "单元格无法可靠读取")
		}
		if kind == excelize.CellTypeNumber || kind == excelize.CellTypeUnset {
			raw, e := f.GetCellValue(sheet, cell, excelize.Options{RawCellValue: true})
			number, parseErr := strconv.ParseFloat(raw, 64)
			if raw != "" && (e != nil || parseErr != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || math.Abs(number) >= 1e15) {
				return nil, result, importError(row, "数字存在精度不确定，请将兑换码转为文本后导入")
			}
		} else if kind == excelize.CellTypeBool || kind == excelize.CellTypeError || kind == excelize.CellTypeDate {
			return nil, result, importError(row, "请将兑换码作为文本填写")
		}
		value, e := f.GetCellValue(sheet, cell)
		code := strings.TrimSpace(value)
		if e != nil || !utf8.ValidString(code) || utf8.RuneCountInString(code) > surveydomain.MaximumTextLength {
			return nil, result, importError(row, "兑换码无法可靠读取或超出答案长度范围")
		}
		if code == "" {
			result.EmptyRows++
			continue
		}
		if seen[code] {
			result.Duplicates++
			continue
		}
		seen[code] = true
		codes = append(codes, code)
	}
	if rows.Error() != nil {
		return nil, result, importError(row, "工作表无法完整读取")
	}
	if len(codes) == 0 {
		return nil, result, importError(0, "没有可导入的兑换码")
	}
	return codes, result, nil
}

func (s *Service) ListRedemptionCodes(ctx context.Context, id surveyport.ID, limit, offset int32) (surveyport.RedemptionPage, error) {
	if s == nil || s.uow == nil || id < 1 || limit < 1 || limit > MaximumLimit || offset < 0 || offset > MaximumOffset {
		return surveyport.RedemptionPage{}, surveyport.ErrInvalid
	}
	store, ok := s.store.(RedemptionStore)
	if !ok {
		return surveyport.RedemptionPage{}, surveyport.ErrUnavailable
	}
	var result surveyport.RedemptionPage
	err := s.uow.Within(ctx, func(tx context.Context) error {
		if _, e := s.store.Get(tx, id, false); e != nil {
			return e
		}
		var e error
		result, e = store.ListRedemptionCodes(tx, id, limit, offset)
		return e
	})
	return result, classify(err)
}

func (s *Service) ImportRedemptionCodes(ctx context.Context, id surveyport.ID, actor int64, key string, content []byte) (surveyport.RedemptionImportResult, error) {
	if s == nil || s.uow == nil || id < 1 || actor < 1 || !validKey(key) {
		return surveyport.RedemptionImportResult{}, surveyport.ErrInvalid
	}
	store, ok := s.store.(RedemptionStore)
	if !ok {
		return surveyport.RedemptionImportResult{}, surveyport.ErrUnavailable
	}
	codes, parsed, err := ParseRedemptionWorkbook(ctx, content)
	if err != nil {
		return parsed, err
	}
	raw, _ := json.Marshal(struct {
		ID                    surveyport.ID
		Codes                 []string
		Duplicates, EmptyRows int
	}{id, codes, parsed.Duplicates, parsed.EmptyRows})
	payload := sha256.Sum256(raw)
	keyDigest := sha256.Sum256([]byte(key))
	now := s.now().UTC()
	result := parsed
	err = s.uow.Within(ctx, func(tx context.Context) error {
		q, e := s.store.Get(tx, id, true)
		if e != nil {
			return e
		}
		if q.Status == surveyport.StatusArchived {
			return surveyport.ErrNotFound
		}
		receipt, owned, e := s.store.Reserve(tx, Reservation{Operation: "redemption_import", ActorScope: fmt.Sprintf("admin:%d", actor), KeyDigest: keyDigest, PayloadDigest: payload, CreatedAt: now})
		if e != nil {
			return e
		}
		if receipt.PayloadDigest != payload {
			return &surveyport.FieldError{Code: "submission_payload_conflict", Message: "导入内容已变化，请重新导入"}
		}
		if !owned {
			if receipt.State != "completed" || json.Unmarshal(receipt.Result, &result) != nil {
				return surveyport.ErrUnavailable
			}
			return nil
		}
		for _, code := range codes {
			added, e := store.InsertRedemptionCode(tx, id, code, actor, now)
			if e != nil {
				return e
			}
			if added {
				result.Imported++
			} else {
				result.Duplicates++
			}
		}
		summary, _ := json.Marshal(result)
		if e = s.store.AppendAuditAndOutbox(tx, "redemption_import", id, fmt.Sprint(actor), summary, "survey.redemption_import:"+hex.EncodeToString(keyDigest[:]), now); e != nil {
			return e
		}
		_, e = s.store.Complete(tx, receipt.ID, summary, now)
		return e
	})
	return result, classify(err)
}
