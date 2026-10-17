package sqlparse

import (
	"fmt"
	"regexp"
	"strings"
)

var dollarQuote = regexp.MustCompile(`^\$[a-zA-Z_][a-zA-Z_0-9]*\$|^\$\$`)

func nameByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// BindNamed never substitutes inside literals, quoted identifiers or comments.
func BindNamed(query, dialect string, params map[string]any) (string, []any, error) {
	var out strings.Builder
	args := []any{}
	for i := 0; i < len(query); {
		if strings.HasPrefix(query[i:], "--") || dialect == "mysql" && query[i] == '#' {
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				out.WriteString(query[i:])
				break
			}
			out.WriteString(query[i : i+end+1])
			i += end + 1
			continue
		}
		if strings.HasPrefix(query[i:], "/*") {
			depth := 1
			j := i + 2
			for j < len(query) && depth > 0 {
				if strings.HasPrefix(query[j:], "/*") {
					depth++
					j += 2
				} else if strings.HasPrefix(query[j:], "*/") {
					depth--
					j += 2
				} else {
					j++
				}
			}
			if depth != 0 {
				return "", nil, fmt.Errorf("SQL 注释未闭合")
			}
			out.WriteString(query[i:j])
			i = j
			continue
		}
		if query[i] == '\'' || query[i] == '"' || query[i] == '`' {
			quote := query[i]
			escapeString := dialect == "mysql" || quote == '\'' && i > 0 && (query[i-1] == 'E' || query[i-1] == 'e') && (i < 2 || !nameByte(query[i-2]))
			j := i + 1
			for j < len(query) {
				if query[j] == '\\' && escapeString {
					j += 2
					continue
				}
				if query[j] == quote {
					j++
					if j < len(query) && query[j] == quote {
						j++
						continue
					}
					break
				}
				j++
			}
			if j > len(query) {
				j = len(query)
			}
			out.WriteString(query[i:j])
			i = j
			continue
		}
		if dialect != "mysql" && query[i] == '$' {
			if tag := dollarQuote.FindString(query[i:]); tag != "" {
				end := strings.Index(query[i+len(tag):], tag)
				if end < 0 {
					return "", nil, fmt.Errorf("SQL 字符串未闭合")
				}
				j := i + len(tag) + end + len(tag)
				out.WriteString(query[i:j])
				i = j
				continue
			}
		}
		name := ""
		end := i
		if query[i] == ':' && (i == 0 || query[i-1] != ':') && i+1 < len(query) && query[i+1] != ':' {
			end = i + 1
			for end < len(query) && nameByte(query[end]) {
				end++
			}
			name = query[i+1 : end]
		}
		if strings.HasPrefix(query[i:], "{{") {
			if n := strings.Index(query[i+2:], "}}"); n >= 0 {
				name = strings.TrimSpace(query[i+2 : i+2+n])
				end = i + 2 + n + 2
			}
		}
		if name != "" {
			value, ok := params[name]
			if !ok {
				return "", nil, fmt.Errorf("缺少参数 %s", name)
			}
			switch value.(type) {
			case nil, string, bool, float64, int, int64:
			default:
				return "", nil, fmt.Errorf("参数必须为标量")
			}
			args = append(args, value)
			if dialect == "mysql" {
				out.WriteByte('?')
			} else {
				fmt.Fprintf(&out, "$%d", len(args))
			}
			i = end
			continue
		}
		out.WriteByte(query[i])
		i++
	}
	return out.String(), args, nil
}
