// Package reportexport serializes approved result snapshots without executing
// another query. Workbooks contain literal values, never executable formulas.
package reportexport

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/user/dbhub/internal/connector"
)

type Snapshot struct {
	WorkspaceID   int64                `json:"workspace_id"`
	RequesterID   int64                `json:"requester_id"`
	RequestID     string               `json:"request_id"`
	ManifestHash  string               `json:"manifest_sha256"`
	ReportID      int64                `json:"report_id"`
	Name          string               `json:"name"`
	Version       int64                `json:"version"`
	PublicationNo int64                `json:"publication_no"`
	CreatedAt     time.Time            `json:"created_at"`
	Params        map[string]any       `json:"params"`
	Labels        map[string]string    `json:"labels,omitempty"`
	Types         map[string]string    `json:"logical_types,omitempty"`
	Unit          string               `json:"unit,omitempty"`
	Currency      string               `json:"currency,omitempty"`
	Timezone      string               `json:"timezone,omitempty"`
	Scope         string               `json:"scope,omitempty"`
	Result        connector.ExecResult `json:"result"`
}

func Encode(snapshot *Snapshot, format, fingerprint string) ([]byte, string, error) {
	if snapshot.Result.Truncated {
		return nil, "", fmt.Errorf("截断结果不能作为完整导出")
	}
	for _, row := range snapshot.Result.Rows {
		if len(row) != len(snapshot.Result.Columns) {
			return nil, "", fmt.Errorf("导出字段与数据行不一致")
		}
	}
	switch format {
	case "json":
		raw, err := json.Marshal(struct {
			Snapshot *Snapshot `json:"snapshot"`
			SHA256   string    `json:"snapshot_sha256"`
		}{snapshot, fingerprint})
		return raw, "application/json; charset=utf-8", err
	case "csv":
		var buffer bytes.Buffer
		buffer.WriteString("\xef\xbb\xbf")
		writer := csv.NewWriter(&buffer)
		writer.UseCRLF = true
		headers := make([]string, len(snapshot.Result.Columns))
		for i, name := range snapshot.Result.Columns {
			headers[i] = spreadsheetText(label(snapshot, name))
		}
		if err := writer.Write(headers); err != nil {
			return nil, "", err
		}
		for _, row := range snapshot.Result.Rows {
			cells := make([]string, len(row))
			for i, value := range row {
				cells[i] = spreadsheetText(cellText(value))
			}
			if err := writer.Write(cells); err != nil {
				return nil, "", err
			}
		}
		writer.Flush()
		return buffer.Bytes(), "text/csv; charset=utf-8", writer.Error()
	case "xlsx":
		raw, err := workbook(snapshot, fingerprint)
		return raw, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", err
	default:
		return nil, "", fmt.Errorf("导出格式仅支持 CSV、JSON 或 Excel")
	}
}
func label(s *Snapshot, name string) string {
	if text := s.Labels[name]; text != "" {
		return text
	}
	return name
}
func cellText(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano)
	case map[string]any, []any:
		raw, _ := json.Marshal(v)
		return string(raw)
	default:
		return fmt.Sprint(v)
	}
}

var numeric = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

func spreadsheetText(text string) string {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) && !numeric.MatchString(trimmed) {
		return "'" + text
	}
	return text
}
func escaped(text string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(text))
	return buffer.String()
}
func coordinate(column, row int) string {
	name := ""
	for column++; column > 0; column = (column - 1) / 26 {
		name = string(rune('A'+(column-1)%26)) + name
	}
	return name + strconv.Itoa(row)
}
func inlineCell(address, text string, style int) (string, error) {
	if utf8.RuneCountInString(text) > 32767 {
		return "", fmt.Errorf("Excel 单元格超过32767字，请选择 CSV 或 JSON")
	}
	for _, r := range text {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' || r == 0xfffe || r == 0xffff {
			return "", fmt.Errorf("Excel 不支持此单元格中的控制字符，请选择 JSON")
		}
	}
	return fmt.Sprintf(`<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, address, style, escaped(text)), nil
}
func dataCell(address string, value any, kind string) (string, error) {
	if value == nil {
		return `<c r="` + address + `"/>`, nil
	}
	if boolean, ok := value.(bool); ok {
		number := "0"
		if boolean {
			number = "1"
		}
		return `<c r="` + address + `" t="b"><v>` + number + `</v></c>`, nil
	}
	text := cellText(value)
	_, number := value.(json.Number)
	switch value.(type) {
	case int, int64, float64:
		number = true
	}
	if kind == "integer" || kind == "number" || kind == "lossless_numeric" {
		number = true
	}
	// Excel stores at most 15 significant decimal digits. Exact long IDs and
	// amounts stay text; they must not be rounded merely by opening a workbook.
	digits := strings.TrimLeft(strings.ReplaceAll(strings.TrimPrefix(text, "-"), ".", ""), "0")
	if number && numeric.MatchString(text) && len(digits) <= 15 {
		style := "0"
		if strings.Contains(text, ".") {
			style = "2"
		}
		return `<c r="` + address + `" s="` + style + `" t="n"><v>` + text + `</v></c>`, nil
	}
	return inlineCell(address, text, 0)
}

const sheetHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`

func workbook(s *Snapshot, fingerprint string) ([]byte, error) {
	if len(s.Result.Columns) > 100 || len(s.Result.Rows) > 100000 {
		return nil, fmt.Errorf("导出超过工作簿支持范围")
	}
	var sheet strings.Builder
	sheet.WriteString(sheetHeader)
	sheet.WriteString(`<sheetViews><sheetView workbookViewId="0"><pane ySplit="1" topLeftCell="A2" activePane="bottomLeft" state="frozen"/></sheetView></sheetViews><cols>`)
	for i, name := range s.Result.Columns {
		width := utf8.RuneCountInString(label(s, name))*2 + 4
		if width < 14 {
			width = 14
		}
		if width > 40 {
			width = 40
		}
		fmt.Fprintf(&sheet, `<col min="%d" max="%d" width="%d" customWidth="1"/>`, i+1, i+1, width)
	}
	sheet.WriteString(`</cols><sheetData><row r="1" ht="25" customHeight="1">`)
	for i, name := range s.Result.Columns {
		cell, err := inlineCell(coordinate(i, 1), label(s, name), 1)
		if err != nil {
			return nil, err
		}
		sheet.WriteString(cell)
	}
	sheet.WriteString(`</row>`)
	for n, row := range s.Result.Rows {
		fmt.Fprintf(&sheet, `<row r="%d">`, n+2)
		for i, value := range row {
			cell, err := dataCell(coordinate(i, n+2), value, s.Types[s.Result.Columns[i]])
			if err != nil {
				return nil, err
			}
			sheet.WriteString(cell)
		}
		sheet.WriteString(`</row>`)
	}
	sheet.WriteString(`</sheetData>`)
	if len(s.Result.Columns) > 0 {
		fmt.Fprintf(&sheet, `<autoFilter ref="A1:%s"/>`, coordinate(len(s.Result.Columns)-1, len(s.Result.Rows)+1))
	}
	sheet.WriteString(`</worksheet>`)
	params, _ := json.Marshal(s.Params)
	info := [][2]string{{"报表", s.Name}, {"报表版本", strconv.FormatInt(s.Version, 10)}, {"发布序号", strconv.FormatInt(s.PublicationNo, 10)}, {"快照生成时间", s.CreatedAt.UTC().Format(time.RFC3339Nano)}, {"完整数据行数", strconv.Itoa(len(s.Result.Rows))}, {"筛选条件", string(params)}, {"数据范围", s.Scope}, {"业务时区", s.Timezone}, {"单位／币种", strings.TrimSpace(s.Unit + " " + s.Currency)}, {"快照SHA256", fingerprint}, {"精度说明", "超过Excel15位有效数字的整数和金额以文本保存；工作簿中不包含公式。"}, {"空值说明", "SQL NULL为空白单元格；JSON格式保留原始类型和NULL。"}, {"字段映射", "显示名称 → 原始字段名"}}
	for _, name := range s.Result.Columns {
		info = append(info, [2]string{label(s, name), name})
	}
	var notes strings.Builder
	notes.WriteString(sheetHeader)
	notes.WriteString(`<cols><col min="1" max="1" width="24" customWidth="1"/><col min="2" max="2" width="90" customWidth="1"/></cols><sheetData>`)
	for i, row := range info {
		fmt.Fprintf(&notes, `<row r="%d">`, i+1)
		for j, text := range row {
			cell, err := inlineCell(coordinate(j, i+1), text, 0)
			if err != nil {
				return nil, err
			}
			notes.WriteString(cell)
		}
		notes.WriteString(`</row>`)
	}
	notes.WriteString(`</sheetData></worksheet>`)
	files := map[string]string{
		"[Content_Types].xml":        `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"_rels/.rels":                `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":            `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><bookViews><workbookView/></bookViews><sheets><sheet name="报表数据" sheetId="1" r:id="rId1"/><sheet name="导出说明" sheetId="2" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`,
		"xl/styles.xml":              `<?xml version="1.0" encoding="UTF-8"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><b/><color rgb="FFFFFFFF"/><sz val="11"/><name val="Calibri"/></font></fonts><fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF26344D"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="1"><border><left/><right/><top/><bottom/><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="3"><xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/><xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFill="1" applyFont="1"/><xf numFmtId="4" fontId="0" fillId="0" borderId="0" xfId="0" applyNumberFormat="1"/></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`,
		"xl/worksheets/sheet1.xml":   sheet.String(), "xl/worksheets/sheet2.xml": notes.String(),
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml", "xl/worksheets/sheet1.xml", "xl/worksheets/sheet2.xml"} {
		file, err := writer.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err = file.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
