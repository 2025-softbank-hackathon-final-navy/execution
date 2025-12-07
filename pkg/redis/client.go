package redis

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/eplb"
	"github.com/redis/go-redis/v9"
)

var Rdb *redis.Client

func InitRedisClient() {
	Rdb = redis.NewClient(&redis.Options{
		Addr: config.REDIS_ADDR,
		DB:   0,
	})
}

type RedisClient interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key string, value string) error
	SetEx(ctx context.Context, key string, value string, ttl int) error
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, seconds int) error
	Scan(ctx context.Context, match string) ([]string, error)
	HGetAll(ctx context.Context, key string) (map[string]string, error)
	HSet(ctx context.Context, key string, values map[string]string) error

	LPush(ctx context.Context, key string, value string) error
	LTrim(ctx context.Context, key string, start, stop int) error
	LRange(ctx context.Context, key string, start, stop int) ([]string, error)
	LLen(ctx context.Context, key string) (int64, error)
}

type GoRedisWrapper struct {
	client *redis.Client
}

// NewRedisWrapper: *redis.Client를 받아서 RedisClient 인터페이스로 반환하는 함수
func NewRedisWrapper(rdb *redis.Client) RedisClient {
	return &GoRedisWrapper{client: rdb}
}

// 아래는 GoRedisWrapper가 RedisClient 인터페이스를 만족하도록 메서드 구현

func (w *GoRedisWrapper) Get(ctx context.Context, key string) (string, error) {
	return w.client.Get(ctx, key).Result()
}

func (w *GoRedisWrapper) Set(ctx context.Context, key string, value string) error {
	return w.client.Set(ctx, key, value, 0).Err()
}

func (w *GoRedisWrapper) SetEx(ctx context.Context, key string, value string, ttl int) error {
	return w.client.Set(ctx, key, value, time.Duration(ttl)*time.Second).Err()
}

func (w *GoRedisWrapper) Incr(ctx context.Context, key string) (int64, error) {
	return w.client.Incr(ctx, key).Result()
}

func (w *GoRedisWrapper) Expire(ctx context.Context, key string, seconds int) error {
	return w.client.Expire(ctx, key, time.Duration(seconds)*time.Second).Err()
}

func (w *GoRedisWrapper) Scan(ctx context.Context, match string) ([]string, error) {
	var keys []string
	// Scan은 커서 방식이므로 모든 키를 가져오려면 반복해야 합니다.
	iter := w.client.Scan(ctx, 0, match, 0).Iterator()
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func (w *GoRedisWrapper) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return w.client.HGetAll(ctx, key).Result()
}

func (w *GoRedisWrapper) LPush(ctx context.Context, key string, value string) error {
	return w.client.LPush(ctx, key, value).Err()
}

func (w *GoRedisWrapper) LTrim(ctx context.Context, key string, start, stop int) error {
	return w.client.LTrim(ctx, key, int64(start), int64(stop)).Err()
}

func (w *GoRedisWrapper) LRange(ctx context.Context, key string, start, stop int) ([]string, error) {
	return w.client.LRange(ctx, key, int64(start), int64(stop)).Result()
}

func (w *GoRedisWrapper) LLen(ctx context.Context, key string) (int64, error) {
	return w.client.LLen(ctx, key).Result()
}

// ============================================================
// Helper Functions (Ported)
// ============================================================
func (w *GoRedisWrapper) HSet(ctx context.Context, key string, values map[string]string) error {
	// go-redis v9의 HSet은 map[string]string을 받아서 처리 가능합니다.
	// (엄밀히는 ...interface{}나 struct, map 등을 지원)
	return w.client.HSet(ctx, key, values).Err()
}

// [추가됨] SetFunctionStatsKey
// 기존에는 테스트 파일에만 있었으나, 실제 운영 코드에서도 통계 생성을 위해 사용할 수 있도록 이식
func SetFunctionStatsKey(ctx context.Context, redis RedisClient, funcID string, stats map[string]string) error {
	key := "function:stats:" + funcID
	// 실제 Redis에서는 HSet만 하면 충분합니다. (Scan은 존재하는 키를 자동으로 찾음)
	return redis.HSet(ctx, key, stats)
}

// ============================================================
// QPS Collection (Updated with Context)
// ============================================================

// CollectQPS collects QPS from Redis metrics
func CollectQPS(ctx context.Context, redis RedisClient, windowSeconds int) map[string]float64 {
	qpsMap := make(map[string]float64)

	statsPattern := "function:stats:*"
	// context를 Scan에 전달
	statsKeys, err := redis.Scan(ctx, statsPattern)
	if err == nil {
		for _, key := range statsKeys {
			funcID := extractFunctionIDFromStatsKey(key)
			if funcID == "" {
				continue
			}

			// context를 전달
			stats, err := getFunctionStats(ctx, redis, funcID)
			if err != nil {
				continue
			}

			if !stats.IsActive {
				log.Printf("[Prewarm] Skipping inactive function: %s (last_executed: %v)",
					funcID, stats.LastExecuted)
				continue
			}

			var qps float64
			if stats.QPS > 0 {
				qps = stats.QPS
			} else if stats.TotalRequests > 0 {
				qps = float64(stats.TotalRequests) / float64(windowSeconds)
			}

			if qps >= eplb.DefaultMinQPS {
				qpsMap[funcID] = qps
			}
		}
	}

	return qpsMap
}

// getFunctionStats retrieves function statistics from Redis
func getFunctionStats(ctx context.Context, redis RedisClient, funcID string) (*eplb.FunctionStats, error) {
	statsKey := fmt.Sprintf("function:stats:%s", funcID)
	// context를 HGetAll에 전달
	statsMap, err := redis.HGetAll(ctx, statsKey)
	if err != nil {
		return nil, err
	}

	stats := &eplb.FunctionStats{
		IsActive: false,
	}

	if totalStr, ok := statsMap["total_requests"]; ok {
		fmt.Sscanf(totalStr, "%d", &stats.TotalRequests)
	}

	if qpsStr, ok := statsMap["qps"]; ok {
		fmt.Sscanf(qpsStr, "%f", &stats.QPS)
	}

	if lastExecStr, ok := statsMap["last_executed"]; ok {
		lastExec, err := time.Parse(time.RFC3339, lastExecStr)
		if err != nil {
			lastExec, err = time.Parse(time.RFC3339, lastExecStr)
		}
		stats.LastExecuted = lastExec

		now := time.Now()
		age := now.Sub(lastExec)
		stats.IsActive = age.Seconds() < float64(eplb.DefaultInactiveThreshold)
	}

	if latencyStr, ok := statsMap["total_latency"]; ok {
		fmt.Sscanf(latencyStr, "%f", &stats.TotalLatency)
	}

	return stats, nil
}

// ... (extractFunctionIDFromStatsKey 유지) ...
func extractFunctionIDFromStatsKey(key string) string {
	prefix := "function:stats:"
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return strings.TrimPrefix(key, prefix)
}

// ============================================================
// EMA Update (Updated with Context)
// ============================================================

// UpdateEMA updates EMA QPS values
func UpdateEMA(ctx context.Context, redis RedisClient, qpsMap map[string]float64, alpha float64) map[string]float64 {
	emaQpsMap := make(map[string]float64)

	for funcID, currentQps := range qpsMap {
		if currentQps < eplb.DefaultMinQPS {
			continue
		}

		stats, err := getFunctionStats(ctx, redis, funcID)
		if err == nil && !stats.IsActive {
			continue
		}

		emaKey := fmt.Sprintf("state:function:%s:ema_qps", funcID)

		// context 전달
		oldEmaStr, err := redis.Get(ctx, emaKey)
		var newEma float64

		if err == nil && oldEmaStr != "" {
			var oldEma float64
			fmt.Sscanf(oldEmaStr, "%f", &oldEma)
			newEma = alpha*currentQps + (1-alpha)*oldEma
		} else {
			newEma = currentQps
		}

		if newEma >= eplb.DefaultMinQPS {
			// context 전달
			redis.Set(ctx, emaKey, fmt.Sprintf("%f", newEma))

			emaHistoryKey := fmt.Sprintf("state:function:%s:ema_qps:history", funcID)
			timestamp := time.Now().Unix()
			historyValue := fmt.Sprintf("%d:%.4f", timestamp, newEma)

			// context 전달
			redis.LPush(ctx, emaHistoryKey, historyValue)
			redis.LTrim(ctx, emaHistoryKey, 0, 59)
			redis.Expire(ctx, emaHistoryKey, 4000)

			emaQpsMap[funcID] = newEma
		}
	}

	return emaQpsMap
}
