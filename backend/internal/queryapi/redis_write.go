package queryapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
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

var errKeyExists = errors.New("Redis key already exists")

func redisString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
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
		client := handle.Client
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		out := map[string]any{"key": in.Key}
		switch operation {
		case "delete":
			var n int64
			n, e = client.Del(ctx, in.Key).Result()
			out["deleted"] = n > 0
		case "ttl":
			if in.TTL == nil || *in.TTL < -1 {
				e = fmt.Errorf("ttl 必须为 -1 或非负秒数")
				break
			}
			var exists bool
			if *in.TTL == -1 {
				exists, e = client.Persist(ctx, in.Key).Result()
			} else {
				exists, e = client.Expire(ctx, in.Key, time.Duration(*in.TTL)*time.Second).Result()
			}
			if e == nil && !exists {
				n, _ := client.Exists(ctx, in.Key).Result()
				if n == 0 {
					e = redis.Nil
				}
			}
			out["ttl"] = *in.TTL
		case "create":
			e = createRedisKey(ctx, client, in)
		case "value":
			e = client.Eval(ctx, `if redis.call('TYPE',KEYS[1]).ok~='string' then return redis.error_reply('WRONGTYPE expected existing string key') end return redis.call('SET',KEYS[1],ARGV[1],'KEEPTTL')`, []string{in.Key}, redisString(in.Value)).Err()
		case "hset", "hdel":
			if in.Field == "" {
				e = fmt.Errorf("field 必填")
				break
			}
			if operation == "hset" {
				e = client.HSet(ctx, in.Key, in.Field, redisString(in.Value)).Err()
			} else {
				e = client.HDel(ctx, in.Key, in.Field).Err()
			}
		case "lpush", "lpop":
			if in.Direction == "" {
				in.Direction = "right"
			}
			if in.Direction != "left" && in.Direction != "right" {
				e = fmt.Errorf("direction 仅支持 left/right")
				break
			}
			if operation == "lpush" {
				if in.Direction == "left" {
					e = client.LPush(ctx, in.Key, redisString(in.Value)).Err()
				} else {
					e = client.RPush(ctx, in.Key, redisString(in.Value)).Err()
				}
			} else {
				var value string
				if in.Direction == "left" {
					value, e = client.LPop(ctx, in.Key).Result()
				} else {
					value, e = client.RPop(ctx, in.Key).Result()
				}
				out["value"] = value
				if errors.Is(e, redis.Nil) {
					out["value"] = nil
					e = nil
				}
			}
		case "sadd":
			e = client.SAdd(ctx, in.Key, in.Member).Err()
		case "srem":
			e = client.SRem(ctx, in.Key, in.Member).Err()
		case "zadd":
			e = client.ZAdd(ctx, in.Key, redis.Z{Score: in.Score, Member: in.Member}).Err()
		case "zrem":
			e = client.ZRem(ctx, in.Key, in.Member).Err()
		default:
			e = fmt.Errorf("不支持该 Redis 操作")
		}
		if e != nil {
			if errors.Is(e, errKeyExists) || errors.Is(e, redis.TxFailedErr) {
				httpx.Fail(w, httpx.Conflict("Redis key 已存在"))
			} else if errors.Is(e, redis.Nil) {
				httpx.Fail(w, httpx.NotFound("Redis key 不存在"))
			} else {
				httpx.Fail(w, httpx.BadRequest("Redis 操作失败: "+safeErr(e)))
			}
			return
		}
		httpx.OK(w, out)
	}
}

func createRedisKey(ctx context.Context, c *redis.Client, in redisInput) error {
	in.Type = strings.ToLower(in.Type)
	if in.TTL != nil && *in.TTL < -1 {
		return fmt.Errorf("ttl 无效")
	}
	return c.Watch(ctx, func(tx *redis.Tx) error {
		n, e := tx.Exists(ctx, in.Key).Result()
		if e != nil {
			return e
		}
		if n > 0 {
			return errKeyExists
		}
		_, e = tx.TxPipelined(ctx, func(p redis.Pipeliner) error {
			switch strings.ToLower(in.Type) {
			case "string":
				p.Set(ctx, in.Key, redisString(in.Value), 0)
			case "hash":
				values, ok := in.Value.(map[string]any)
				if !ok || len(values) == 0 {
					return fmt.Errorf("hash value 必须是非空对象")
				}
				fields := map[string]any{}
				for k, v := range values {
					fields[k] = redisString(v)
				}
				p.HSet(ctx, in.Key, fields)
			case "list", "set", "zset":
				values, ok := in.Value.([]any)
				if !ok || len(values) == 0 {
					return fmt.Errorf("value 必须是非空数组")
				}
				if len(values) > 10000 {
					return fmt.Errorf("元素过多")
				}
				for _, v := range values {
					switch in.Type {
					case "list":
						p.RPush(ctx, in.Key, redisString(v))
					case "set":
						p.SAdd(ctx, in.Key, redisString(v))
					case "zset":
						item, ok := v.(map[string]any)
						if !ok {
							return fmt.Errorf("zset 元素必须包含 member/score")
						}
						score, ok := item["score"].(float64)
						if !ok {
							return fmt.Errorf("zset score 必须为数字")
						}
						p.ZAdd(ctx, in.Key, redis.Z{Score: score, Member: redisString(item["member"])})
					}
				}
			default:
				return fmt.Errorf("type 仅支持 string/hash/list/set/zset")
			}
			if in.TTL != nil && *in.TTL >= 0 {
				p.Expire(ctx, in.Key, time.Duration(*in.TTL)*time.Second)
			}
			return nil
		})
		return e
	}, in.Key)
}
