package native

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/user/dbhub/internal/connector"
	"strings"
	"time"
)

var errKeyExists = errors.New("Redis key already exists")

func redisString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}
func (s *keySession) Mutate(ctx context.Context, operation string, in connector.KeyMutation) (map[string]any, error) {
	client := s.r.Client
	var e error
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

	if errors.Is(e, errKeyExists) || errors.Is(e, redis.TxFailedErr) {
		e = &connector.Error{Kind: "conflict", Message: "Redis key 已存在", Cause: e}
	}
	return out, normalize(e)
}
func createRedisKey(ctx context.Context, c *redis.Client, in connector.KeyMutation) error {
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
