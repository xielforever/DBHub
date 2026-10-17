// Package presenceapi exposes authenticated, expiring online presence in Redis.
package presenceapi

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/auth"
	"github.com/user/dbhub/internal/db"
	"github.com/user/dbhub/internal/httpx"
)

type Handler struct {
	Redis *redis.Client
	Conns *db.ConnectionRepository
}

func (h *Handler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ConnectionID int64 `json:"connection_id"`
	}
	if e := httpx.DecodeJSON(w, r, &in); e != nil {
		httpx.Fail(w, e)
		return
	}
	h.run(w, r, in.ConnectionID, true)
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("connection_id"), 10, 64)
	h.run(w, r, id, false)
}
func (h *Handler) run(w http.ResponseWriter, r *http.Request, id int64, beat bool) {
	if _, e := h.Conns.Get(r.Context(), id); e != nil {
		httpx.Fail(w, httpx.NotFound("数据源不存在"))
		return
	}
	if h.Redis == nil {
		httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "在线会话需要 Redis 服务"})
		return
	}
	key := fmt.Sprintf("dbhub:presence:%d", id)
	c, _ := auth.ClaimsFromContext(r.Context())
	now := time.Now().Unix()
	ctx := r.Context()
	pipe := h.Redis.TxPipeline()
	if beat {
		pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: c.Username})
	}
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(now-45, 10))
	pipe.Expire(ctx, key, 90*time.Second)
	list := pipe.ZRange(ctx, key, 0, -1)
	if _, e := pipe.Exec(ctx); e != nil {
		httpx.JSON(w, 503, httpx.Envelope{Code: 50300, Message: "在线会话服务暂不可用"})
		return
	}
	users := []map[string]any{}
	for _, name := range list.Val() {
		users = append(users, map[string]any{"name": name, "self": name == c.Username})
	}
	httpx.OK(w, map[string]any{"items": users, "ttl_seconds": 45})
}
