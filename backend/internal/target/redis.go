package target

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisOverviewInfo 解析 INFO 输出，返回概览指标。
type RedisOverview struct {
	Version          string         `json:"version"`
	Mode             string         `json:"mode"`
	OS               string         `json:"os"`
	UptimeDays       int64          `json:"uptime_days"`
	ConnectedClients int64          `json:"connected_clients"`
	UsedMemoryMB     float64        `json:"used_memory_mb"`
	TotalCommands    int64          `json:"total_commands"`
	Keyspaces        []KeyspaceInfo `json:"keyspaces"`
}

// KeyspaceInfo 单个 Redis 逻辑库的键统计。
type KeyspaceInfo struct {
	DB      string `json:"db"`
	Keys    int64  `json:"keys"`
	Expires int64  `json:"expires"`
}

// RedisKeyItem 键扫描结果项。
type RedisKeyItem struct {
	Key  string `json:"key"`
	Type string `json:"type"`
	TTL  int64  `json:"ttl"`
}

// RedisKeyInspection 键值检视结果。
type RedisKeyInspection struct {
	Type  string `json:"type"`
	TTL   int64  `json:"ttl"`
	Size  int64  `json:"size"`
	Value any    `json:"value"`
}

// collectionCap 各集合类型最多返回的元素数。
const collectionCap = 100

// RedisOverviewInfo 读取并解析 Redis INFO。
func RedisOverviewInfo(ctx context.Context, c *redis.Client) (*RedisOverview, error) {
	info, err := c.Info(ctx).Result()
	if err != nil {
		return nil, err
	}
	kv := parseRedisInfo(info)
	out := &RedisOverview{
		Version:          kv["redis_version"],
		Mode:             kv["redis_mode"],
		OS:               kv["os"],
		UptimeDays:       atoi64(kv["uptime_in_days"]),
		ConnectedClients: atoi64(kv["connected_clients"]),
		UsedMemoryMB:     float64(atoi64(kv["used_memory"])) / 1024 / 1024,
		TotalCommands:    atoi64(kv["total_commands_processed"]),
		Keyspaces:        make([]KeyspaceInfo, 0),
	}
	// INFO 的 Keyspace 段为 db0:keys=..,expires=..,avg_ttl=..
	for line := range infoLines(info) {
		if !strings.HasPrefix(line, "db") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		name := line[:colon]
		fields := parseKV(line[colon+1:], ',', '=')
		out.Keyspaces = append(out.Keyspaces, KeyspaceInfo{
			DB:      name,
			Keys:    atoi64(fields["keys"]),
			Expires: atoi64(fields["expires"]),
		})
	}
	sort.Slice(out.Keyspaces, func(i, j int) bool { return out.Keyspaces[i].DB < out.Keyspaces[j].DB })
	return out, nil
}

// infoLines 以通道方式遍历 INFO 文本行（保留独立可测的小工具形态）。
func infoLines(s string) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		for _, line := range strings.Split(s, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			ch <- line
		}
	}()
	return ch
}

// parseRedisInfo 解析 INFO 默认段为扁平 map。
func parseRedisInfo(info string) map[string]string {
	out := make(map[string]string)
	for line := range infoLines(info) {
		if strings.HasPrefix(line, "db") && strings.Contains(line, ":keys=") {
			continue // keyspace 行单独处理
		}
		if i := strings.IndexByte(line, ':'); i > 0 {
			out[line[:i]] = line[i+1:]
		}
	}
	return out
}

func parseKV(s string, pairSep, kvSep byte) map[string]string {
	out := make(map[string]string)
	for _, pair := range strings.Split(s, string(pairSep)) {
		if i := strings.IndexByte(pair, kvSep); i > 0 {
			out[pair[:i]] = pair[i+1:]
		}
	}
	return out
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// ScanKeys 使用 SCAN 增量扫描键，返回类型与 TTL（最多 limit 个）。
func ScanKeys(ctx context.Context, c *redis.Client, pattern string, limit int64) ([]RedisKeyItem, error) {
	if pattern == "" {
		pattern = "*"
	}
	if limit <= 0 {
		limit = 200
	}
	out := make([]RedisKeyItem, 0, limit)
	var cursor uint64
	for {
		keys, next, err := c.Scan(ctx, cursor, pattern, 200).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			if int64(len(out)) >= limit {
				return out, nil
			}
			pipe := c.Pipeline()
			typeCmd := pipe.Type(ctx, key)
			ttlCmd := pipe.TTL(ctx, key)
			if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
				return nil, err
			}
			out = append(out, RedisKeyItem{
				Key:  key,
				Type: typeCmd.Val(),
				TTL:  ttlSeconds(ttlCmd.Val()),
			})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return out, nil
}

// ttlSeconds 将 go-redis 的 Duration 转为秒级语义：-1 永久、-2 不存在。
func ttlSeconds(d time.Duration) int64 {
	switch {
	case d < -time.Nanosecond: // -2 不存在 / -1 永久
		return int64(d.Seconds())
	case d < 0:
		return -1
	default:
		return int64(d.Seconds())
	}
}

// InspectKey 按类型读取一个键的内容（集合类型最多取 collectionCap 条）。
func InspectKey(ctx context.Context, c *redis.Client, key string) (*RedisKeyInspection, error) {
	keyType, err := c.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if keyType == "none" {
		return nil, fmt.Errorf("键不存在或已过期")
	}
	ttl := ttlSeconds(c.TTL(ctx, key).Val())
	out := &RedisKeyInspection{Type: keyType, TTL: ttl}

	switch keyType {
	case "string":
		v, err := c.Get(ctx, key).Result()
		if err != nil && err != redis.Nil {
			return nil, err
		}
		out.Value = v
		out.Size = int64(len(v))
	case "list":
		n, err := c.LLen(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		vals, err := c.LRange(ctx, key, 0, collectionCap-1).Result()
		if err != nil {
			return nil, err
		}
		out.Size = n
		out.Value = vals
	case "hash":
		n, err := c.HLen(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		m, err := c.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		if int64(len(m)) > collectionCap {
			m = capMap(m, collectionCap)
		}
		out.Size = n
		out.Value = m
	case "set":
		n, err := c.SCard(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		members, err := c.SRandMemberN(ctx, key, collectionCap).Result()
		if err != nil {
			return nil, err
		}
		out.Size = n
		out.Value = members
	case "zset":
		n, err := c.ZCard(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		zs, err := c.ZRangeWithScores(ctx, key, 0, collectionCap-1).Result()
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(zs))
		for _, z := range zs {
			items = append(items, map[string]any{"member": z.Member, "score": z.Score})
		}
		out.Size = n
		out.Value = items
	case "stream":
		n, err := c.XLen(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		msgs, err := c.XRangeN(ctx, key, "-", "+", collectionCap).Result()
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(msgs))
		for _, m := range msgs {
			items = append(items, map[string]any{"id": m.ID, "fields": m.Values})
		}
		out.Size = n
		out.Value = items
	default:
		out.Value = "暂不支持预览该类型的键"
	}
	return out, nil
}

// capMap 将 map 截断到 n 条（键名排序保证稳定）。
func capMap(m map[string]string, n int) map[string]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, n)
	for i, k := range keys {
		if i >= n {
			break
		}
		out[k] = m[k]
	}
	return out
}
