package target

import (
	"math"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
)

// JSON numbers otherwise become IEEE-754 values in the browser. Preserve
// PostgreSQL numeric scale and unsafe integers as text for reports. Structured
// JSON cells use the original wire text, before pgx's generic float decoding.
func losslessPGValue(value any, raw []byte, oid uint32, format int16) any {
	if oid == pgtype.JSONOID || oid == pgtype.JSONBOID {
		if raw == nil {
			return nil
		}
		if oid == pgtype.JSONBOID && format == pgtype.BinaryFormatCode && len(raw) > 0 && raw[0] == 1 {
			raw = raw[1:]
		}
		return string(raw)
	}
	if value == nil {
		return nil
	}
	return losslessNumber(value)
}

func losslessNumber(value any) any {
	const maxSafe = 9007199254740991
	switch n := value.(type) {
	case int64:
		if n > maxSafe || n < -maxSafe {
			return strconv.FormatInt(n, 10)
		}
	case uint64:
		if n > maxSafe {
			return strconv.FormatUint(n, 10)
		}
	case pgtype.Numeric:
		text, err := n.Value()
		if err == nil {
			return text
		}
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) {
			return strconv.FormatFloat(n, 'g', -1, 64)
		}
	case float32:
		if math.IsInf(float64(n), 0) || math.IsNaN(float64(n)) {
			return strconv.FormatFloat(float64(n), 'g', -1, 32)
		}
	case []any:
		for i, cell := range n {
			n[i] = losslessNumber(cell)
		}
		return n
	}
	return value
}
