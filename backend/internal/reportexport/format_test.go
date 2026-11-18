package reportexport

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/user/dbhub/internal/connector"
)

func sampleSnapshot() *Snapshot {
	return &Snapshot{Name: "财务对账", CreatedAt: time.Date(2026, 10, 20, 1, 0, 0, 0, time.UTC), Params: map[string]any{"q": "订单"}, Labels: map[string]string{"id": "编号", "amount": "金额", "text": "说明"}, Types: map[string]string{"id": "integer", "amount": "lossless_numeric"}, Result: connector.ExecResult{Columns: []string{"id", "amount", "text"}, Rows: [][]any{{"9007199254740993", "12345678901234567890.123456789", "=HYPERLINK(\"https://invalid.test\",\"x\")"}, {json.Number("2"), "-12.34", nil}}}}
}
func TestExportsPreservePrecisionAndLiteralText(t *testing.T) {
	snapshot := sampleSnapshot()
	data, _, err := Encode(snapshot, "json", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("9007199254740993")) || !bytes.Contains(data, []byte("12345678901234567890.123456789")) {
		t.Fatal("JSON lost precision")
	}
	data, _, err = Encode(snapshot, "csv", "hash")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff"))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][0] != "编号" || rows[1][0] != "9007199254740993" || !strings.HasPrefix(rows[1][2], "'=HYPERLINK") || rows[2][1] != "-12.34" {
		t.Fatal("CSV changed precision or allowed formulas", rows)
	}
	data, _, err = Encode(snapshot, "xlsx", "hash")
	if err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var sheet string
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(file.Name, ".xml") {
			decoder := xml.NewDecoder(bytes.NewReader(raw))
			for {
				_, err = decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("invalid workbook XML %s: %v", file.Name, err)
				}
			}
		}
		if file.Name == "xl/worksheets/sheet1.xml" {
			sheet = string(raw)
		}
	}
	for _, text := range []string{`t="inlineStr"`, `9007199254740993`, `12345678901234567890.123456789`, `=HYPERLINK`} {
		if !strings.Contains(sheet, text) {
			t.Fatal("workbook lost literal or exact value", text)
		}
	}
	if strings.Contains(sheet, "<f>") {
		t.Fatal("workbook contains executable formula")
	}
	if !strings.Contains(sheet, `<c r="B3" s="2" t="n"><v>-12.34</v></c>`) {
		t.Fatal("ordinary monetary amount was not numeric")
	}
}
func TestExportsRejectIncompleteAndUnsupportedCells(t *testing.T) {
	snapshot := sampleSnapshot()
	snapshot.Result.Truncated = true
	for _, format := range []string{"csv", "json", "xlsx"} {
		if _, _, err := Encode(snapshot, format, "hash"); err == nil {
			t.Fatal("partial snapshot exported", format)
		}
	}
	snapshot.Result.Truncated = false
	snapshot.Result.Rows[0][2] = strings.Repeat("中", 32768)
	if _, _, err := Encode(snapshot, "xlsx", "hash"); err == nil {
		t.Fatal("Excel silently truncated a long cell")
	}
	snapshot.Result.Rows[0][2] = "x\x00y"
	if _, _, err := Encode(snapshot, "xlsx", "hash"); err == nil {
		t.Fatal("Excel silently changed invalid XML content")
	}
	if _, _, err := Encode(snapshot, "pdf", "hash"); err == nil {
		t.Fatal("unknown format accepted")
	}
}
