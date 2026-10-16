package target

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

//go:embed compatibility_matrix.json
var compatibilityMatrixJSON []byte

type TestedVersion struct {
	Engine        string `json:"engine"`
	Branch        string `json:"branch"`
	TestedVersion string `json:"tested_version"`
}

// Compatibility 描述连接所报告的版本和验证范围，不等同于所有 SQL/部署模式均受支持。
type Compatibility struct {
	Engine        string `json:"engine"`
	Product       string `json:"product"`
	Version       string `json:"version"`
	Branch        string `json:"branch"`
	Status        string `json:"status"`
	TestedVersion string `json:"tested_version,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Message       string `json:"message"`
}

var numericVersion = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?`)
var tidbVersion = regexp.MustCompile(`(?i)tidb[- ]v?(\d+\.\d+\.\d+)`)
var prereleaseVersion = regexp.MustCompile(`(?i)(alpha|beta|devel|(?:^|[-_.0-9])rc)[0-9]*`)

func TestedVersions() []TestedVersion {
	var matrix []TestedVersion
	// 只读内嵌数据；每次解码返回独立副本，调用方无法改写全局验证结论。
	if err := json.Unmarshal(compatibilityMatrixJSON, &matrix); err != nil {
		panic("无效的数据源兼容矩阵")
	}
	return matrix
}

func classifyCompatibility(kind, raw, comment, mode string) Compatibility {
	return classifyWithMatrix(kind, raw, comment, mode, TestedVersions())
}

func classifyWithMatrix(kind, raw, comment, mode string, matrix []TestedVersion) Compatibility {
	c := Compatibility{Engine: kind, Product: map[string]string{"mysql": "MySQL", "postgres": "PostgreSQL", "redis": "Redis"}[kind], Status: "unknown", Mode: mode}
	text := strings.ToLower(raw + " " + comment)
	versionText := strings.TrimSpace(raw)
	// 协议兼容不代表产品已验证。先识别常见分支，避免 MariaDB 的 5.5.5 前缀误判。
	if kind == "mysql" {
		for _, fork := range []struct{ marker, engine, product string }{
			{"mariadb", "mariadb", "MariaDB"}, {"tidb", "tidb", "TiDB"},
			{"percona", "percona", "Percona Server"}, {"oceanbase", "oceanbase", "OceanBase"},
			{"aurora", "aurora-mysql", "Aurora MySQL"}, {"polardb", "polardb", "PolarDB"},
		} {
			if strings.Contains(text, fork.marker) {
				c.Engine, c.Product = fork.engine, fork.product
				break
			}
		}
		if c.Engine == "mariadb" {
			versionText = strings.TrimPrefix(versionText, "5.5.5-")
		}
		if c.Engine == "tidb" {
			if match := tidbVersion.FindStringSubmatch(raw); match != nil {
				versionText = match[1]
			}
		}
	}
	if kind == "postgres" {
		for _, fork := range []struct{ marker, engine, product string }{
			{"cockroachdb", "cockroachdb", "CockroachDB"}, {"yugabyte", "yugabytedb", "YugabyteDB"},
			{"enterprisedb", "enterprisedb", "EnterpriseDB"}, {"aurora", "aurora-postgres", "Aurora PostgreSQL"},
		} {
			if strings.Contains(text, fork.marker) {
				c.Engine, c.Product = fork.engine, fork.product
				break
			}
		}
		if strings.HasPrefix(strings.ToLower(versionText), "postgresql ") {
			versionText = strings.TrimSpace(versionText[len("PostgreSQL "):])
		}
	}
	if kind == "redis" {
		for _, fork := range []struct{ marker, engine, product string }{
			{"valkey", "valkey", "Valkey"}, {"keydb", "keydb", "KeyDB"}, {"dragonfly", "dragonfly", "Dragonfly"},
		} {
			if strings.Contains(text, fork.marker) {
				c.Engine, c.Product = fork.engine, fork.product
				break
			}
		}
	}
	fields := strings.Fields(versionText)
	if len(fields) > 0 {
		versionText = fields[0]
	}
	if match := numericVersion.FindStringSubmatch(versionText); match != nil {
		major, e1 := strconv.Atoi(match[1])
		minor, e2 := strconv.Atoi(match[2])
		if e1 == nil && e2 == nil && major > 0 {
			c.Version = fmt.Sprintf("%d.%d", major, minor)
			c.Branch = c.Version
			if match[3] != "" {
				patch, err := strconv.Atoi(match[3])
				if err == nil {
					c.Version += fmt.Sprintf(".%d", patch)
				}
			}
			if kind == "postgres" && major >= 10 {
				c.Branch = strconv.Itoa(major)
			}
		}
	}
	if c.Engine != kind {
		c.Status, c.Message = "unverified", c.Product+" 尚未纳入兼容验证，不能按相同协议的数据库版本判断支持情况。"
		return c
	}
	if kind == "redis" && mode != "standalone" {
		c.Status, c.Message = "unverified", "当前仅验证 Redis 单节点；Cluster、Sentinel 或未知运行模式尚未验证。"
		return c
	}
	if c.Version == "" || c.Product == "" {
		c.Message = "连接已建立，但无法可靠识别服务端版本；兼容性尚未确认。"
		return c
	}
	c.Status, c.Message = "unverified", "当前版本尚未纳入兼容验证，请先核实业务 SQL 或键值操作。"
	if prereleaseVersion.MatchString(versionText) {
		c.Message = "预发布版本尚未纳入兼容验证。"
		return c
	}
	for _, item := range matrix {
		if item.Engine == kind && item.Branch == c.Branch && item.TestedVersion != "" {
			c.TestedVersion = item.TestedVersion
			c.Status, c.Message = "branch_verified", "该版本分支已有基础兼容实测，当前补丁版本尚未单独验证。"
			if c.Version == item.TestedVersion {
				c.Status, c.Message = "verified", "该版本已通过基础兼容验证。"
			}
			return c
		}
	}
	return c
}
