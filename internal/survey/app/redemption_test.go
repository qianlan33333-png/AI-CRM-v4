package app

import (
	"context"
	"errors"
	"testing"

	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
	"github.com/xuri/excelize/v2"
)

func workbookForRedemption(t *testing.T, write func(*excelize.File)) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer f.Close()
	if err := f.SetCellStr("Sheet1", "A1", "兑换码"); err != nil {
		t.Fatal(err)
	}
	write(f)
	data, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func TestRedemptionWorkbookPreservesTextAndSkipsDuplicates(t *testing.T) {
	data := workbookForRedemption(t, func(f *excelize.File) {
		for cell, value := range map[string]string{"A2": " 00123 ", "A3": "00123", "A4": "abc", "A5": "ABC", "A6": " ", "A7": "末行"} {
			if err := f.SetCellStr("Sheet1", cell, value); err != nil {
				t.Fatal(err)
			}
		}
	})
	codes, result, err := ParseRedemptionWorkbook(context.Background(), data)
	if err != nil || len(codes) != 4 || codes[0] != "00123" || codes[1] != "abc" || codes[2] != "ABC" || result.Duplicates != 1 || result.EmptyRows != 1 {
		t.Fatalf("codes=%v counts=%+v err=%v", codes, result, err)
	}
}
func TestRedemptionWorkbookRejectsFormulaPrecisionAndCorruption(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		write func(*excelize.File)
	}{
		{"formula", func(f *excelize.File) { _ = f.SetCellFormula("Sheet1", "A3", `"CODE"`) }},
		{"precision", func(f *excelize.File) { _ = f.SetCellValue("Sheet1", "A3", float64(1234567890123456)) }},
		{"bad_header", func(f *excelize.File) { _ = f.SetCellStr("Sheet1", "A1", "错误列") }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			data := workbookForRedemption(t, func(f *excelize.File) { _ = f.SetCellStr("Sheet1", "A2", "先前有效码"); scenario.write(f) })
			codes, _, err := ParseRedemptionWorkbook(context.Background(), data)
			var field *surveyport.FieldError
			if len(codes) != 0 || !errors.As(err, &field) || field.Code != "redemption_import_invalid" {
				t.Fatalf("codes=%v err=%v", codes, err)
			}
		})
	}
	if codes, _, err := ParseRedemptionWorkbook(context.Background(), []byte("not-xlsx")); err == nil || len(codes) != 0 {
		t.Fatal("corrupt workbook accepted")
	}
}
func TestRedemptionWorkbookPreservesFormattedNumericLeadingZeros(t *testing.T) {
	data := workbookForRedemption(t, func(f *excelize.File) {
		_ = f.SetCellInt("Sheet1", "A2", 123)
		format := "00000"
		style, err := f.NewStyle(&excelize.Style{CustomNumFmt: &format})
		if err != nil {
			t.Fatal(err)
		}
		_ = f.SetCellStyle("Sheet1", "A2", "A2", style)
	})
	codes, _, err := ParseRedemptionWorkbook(context.Background(), data)
	if err != nil || len(codes) != 1 || codes[0] != "00123" {
		t.Fatalf("codes=%v err=%v", codes, err)
	}
}
