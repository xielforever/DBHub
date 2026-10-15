package target

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/redis/go-redis/v9"
)

func infoMap(info string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[k] = v
		}
	}
	return out
}
func RedisOverviewInfo(ctx context.Context, client *redis.Client) (*RedisInfo, error) {
	text, e := client.Info(ctx, "server", "clients", "memory", "stats", "keyspace").Result()
	if e != nil {
		return nil, fmt.Errorf("读取 Redis INFO 失败")
	}
	data := infoMap(text)
	number := func(k string) int64 { n, _ := strconv.ParseInt(data[k], 10, 64); return n }
	out := &RedisInfo{Version: data["redis_version"], Mode: data["redis_mode"], OS: data["os"], UptimeDays: int(number("uptime_in_days")), ConnectedClients: int(number("connected_clients")), UsedMemoryMB: int(number("used_memory") / (1024 * 1024)), UsedMemoryHuman: data["used_memory_human"], TotalCommands: number("total_commands_processed"), Keyspaces: []any{}}
	for key, value := range data {
		if !strings.HasPrefix(key, "db") {
			continue
		}
		parts := map[string]int64{}
		for _, pair := range strings.Split(value, ",") {
			k, v, ok := strings.Cut(pair, "=")
			if ok {
				parts[k], _ = strconv.ParseInt(v, 10, 64)
			}
		}
		out.Keyspaces = append(out.Keyspaces, map[string]any{"db": key, "keys": parts["keys"], "expires": parts["expires"]})
		out.TotalKeys += parts["keys"]
	}
	return out, nil
}
func ScanKeys(ctx context.Context, client *redis.Client, pattern string, max int64) ([]RedisKeyItem, error) {
	if pattern == "" {
		pattern = "*"
	}
	if max <= 0 || max > 10000 {
		max = 200
	}
	out := []RedisKeyItem{}
	iter := client.Scan(ctx, 0, pattern, 100).Iterator()
	seen := map[string]bool{}
	for iter.Next(ctx) {
		key := iter.Val()
		if seen[key] {
			continue
		}
		seen[key] = true
		typ, e := client.Type(ctx, key).Result()
		if e != nil {
			return nil, e
		}
		if typ == "none" {
			continue
		}
		ttl, e := client.TTL(ctx, key).Result()
		if e != nil {
			return nil, e
		}
		seconds := int64(ttl.Seconds())
		if ttl < 0 {
			seconds = -1
		}
		out = append(out, RedisKeyItem{Key: key, Type: typ, TTL: seconds})
		if int64(len(out)) >= max {
			break
		}
	}
	return out, iter.Err()
}
func InspectKey(ctx context.Context, client *redis.Client, key string) (*RedisValue, error) {
	typ, e := client.Type(ctx, key).Result()
	if e != nil {
		return nil, e
	}
	if typ == "none" {
		return nil, redis.Nil
	}
	ttl, e := client.TTL(ctx, key).Result()
	if e != nil {
		return nil, e
	}
	seconds := int64(ttl.Seconds())
	if ttl < 0 {
		seconds = -1
	}
	var value any
	switch typ {
	case "string":
		value, e = client.Get(ctx, key).Result()
	case "hash":
		value, e = client.HGetAll(ctx, key).Result()
	case "list":
		value, e = client.LRange(ctx, key, 0, 9999).Result()
	case "set":
		value, e = client.SMembers(ctx, key).Result()
	case "zset":
		var members []redis.Z
		members, e = client.ZRangeWithScores(ctx, key, 0, 9999).Result()
		out := []map[string]any{}
		for _, m := range members {
			out = append(out, map[string]any{"member": m.Member, "score": m.Score})
		}
		value = out
	default:
		return nil, fmt.Errorf("暂不支持该 Redis 数据类型")
	}
	if e != nil {
		return nil, e
	}
	size, e := client.MemoryUsage(ctx, key).Result()
	if e != nil {
		return nil, e
	}
	return &RedisValue{Key: key, Type: typ, TTL: seconds, Size: size, Value: value}, nil
}
