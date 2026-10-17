package queryapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/user/dbhub/internal/connector"
	target "github.com/user/dbhub/internal/connectorruntime"
	"github.com/user/dbhub/internal/httpx"
)

type redisInput struct {
	ConnectionID int64   `json:"connection_id"`
	Key          string  `json:"key"`
	Type         string  `json:"type"`
	Value        any     `json:"value"`
	TTL          *int64  `json:"ttl"`
	Field        string  `json:"field"`
	Member       string  `json:"member"`
	Score        float64 `json:"score"`
	Direction    string  `json:"direction"`
}

func (h *Handler) RedisMutation(operation string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in redisInput
		if r.ContentLength != 0 {
			if e := httpx.DecodeJSON(w, r, &in); e != nil {
				httpx.Fail(w, e)
				return
			}
		}
		if in.ConnectionID == 0 {
			in.ConnectionID, _ = strconv.ParseInt(r.URL.Query().Get("connection_id"), 10, 64)
		}
		if in.Key == "" {
			in.Key = r.URL.Query().Get("key")
		}
		if in.Field == "" {
			in.Field = r.URL.Query().Get("field")
		}
		if in.Member == "" {
			in.Member = r.URL.Query().Get("member")
		}
		if in.ConnectionID < 1 || in.Key == "" {
			httpx.Fail(w, httpx.BadRequest("connection_id/key 必填"))
			return
		}
		handle, cleanup, e := h.openRedis(r, in.ConnectionID)
		if e != nil {
			httpx.Fail(w, e)
			return
		}
		defer cleanup()
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		out, e := target.MutateKey(ctx, handle, operation, connector.KeyMutation{Key: in.Key, Type: in.Type, Value: in.Value, TTL: in.TTL, Field: in.Field, Member: in.Member, Score: in.Score, Direction: in.Direction})

		if e != nil {
			var ce *connector.Error
			if errors.As(e, &ce) && ce.Kind == "conflict" {
				httpx.Fail(w, httpx.Conflict("Redis key 已存在"))
			} else if errors.As(e, &ce) && ce.Kind == "not_found" {
				httpx.Fail(w, httpx.NotFound("Redis key 不存在"))
			} else {
				httpx.Fail(w, httpx.BadRequest("Redis 操作失败: "+safeErr(e)))
			}
			return
		}
		httpx.OK(w, out)
	}
}
