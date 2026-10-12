package app

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	surveydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/domain"
	"github.com/xuri/excelize/v2"
)

func FuzzRedemptionWorkbookMalformedUploadFailsAtomically(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("PK\x03\x04broken spreadsheet"))
	book := excelize.NewFile()
	_ = book.SetCellStr("Sheet1", "A1", "兑换码")
	_ = book.SetCellStr("Sheet1", "A2", "00123")
	_ = book.SetCellStr("Sheet1", "A3", "00123")
	buffer, err := book.WriteToBuffer()
	_ = book.Close()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(buffer.Bytes())
	f.Fuzz(func(t *testing.T, content []byte) {
		if len(content) > 32<<10 {
			return
		}
		codes, result, err := ParseRedemptionWorkbook(context.Background(), content)
		if err != nil {
			if len(codes) != 0 {
				t.Fatal("malformed workbook returned partially accepted qualification codes")
			}
			return
		}
		if len(codes) == 0 || result.Imported != 0 || result.EmptyRows < 0 || result.Duplicates < 0 {
			t.Fatal("workbook parser fabricated import effects or invalid counts")
		}
		seen := map[string]bool{}
		for _, code := range codes {
			if code == "" || strings.TrimSpace(code) != code || !utf8.ValidString(code) || utf8.RuneCountInString(code) > surveydomain.MaximumTextLength || seen[code] {
				t.Fatal("accepted workbook breaks qualification-code contract")
			}
			seen[code] = true
		}
	})
}
