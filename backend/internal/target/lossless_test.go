package target

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestReportWirePrecision(t *testing.T) {
	var numeric pgtype.Numeric
	if err := numeric.Scan("12345678901234567890.123456789"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name  string
		input any
		want  string
	}{
		{"large-integer", int64(9007199254740993), `"9007199254740993"`},
		{"negative-large-integer", int64(-9007199254740993), `"-9007199254740993"`},
		{"safe-integer", int64(123), `123`},
		{"decimal", numeric, `"12345678901234567890.123456789"`},
		{"null", nil, `null`},
		{"nan", math.NaN(), `"NaN"`},
	} {
		t.Run(item.name, func(t *testing.T) {
			raw, err := json.Marshal(losslessNumber(item.input))
			if err != nil || string(raw) != item.want {
				t.Fatal(string(raw), err)
			}
		})
	}
	text := `{"id":9007199254740993,"amount":12345678901234567890.123456789}`
	if got := losslessPGValue(map[string]any{"id": float64(9007199254740993)}, append([]byte{1}, []byte(text)...), pgtype.JSONBOID, pgtype.BinaryFormatCode); got != text {
		t.Fatal("JSON cell lost its original wire values", got)
	}
	if got := losslessPGValue(nil, []byte("null"), pgtype.JSONOID, pgtype.TextFormatCode); got != "null" {
		t.Fatal("JSON null was confused with SQL NULL", got)
	}
}
