package target

import (
	"context"
	"database/sql"

	"github.com/user/dbhub/internal/db"
)

// Test 测试目标数据源连通性并返回服务端版本信息（短连接，调用即释放）。
func Test(ctx context.Context, conn *db.Connection, password string, tunnel *db.SSHTunnel) (*TestInfo, error) {
	info := &TestInfo{Type: conn.Type}
	switch conn.Type {
	case "redis":
		c, err := OpenRedis(ctx, conn, password, tunnel)
		if err != nil {
			return nil, err
		}
		defer c.Close()
		ov, err := RedisOverviewInfo(ctx, c.Client)
		if err != nil {
			return nil, err
		}
		info.Version = ov.Version
		return info, nil
	default:
		h, err := OpenRelational(ctx, conn, password, tunnel)
		if err != nil {
			return nil, err
		}
		defer h.Close()
		q := "SELECT VERSION()"
		if conn.Type == "postgres" {
			q = "SELECT version()"
		}
		var version sql.NullString
		if err := h.db.QueryRowContext(ctx, q).Scan(&version); err != nil {
			return nil, err
		}
		info.Version = truncate(version.String, 80)
		return info, nil
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
