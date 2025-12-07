package monitor

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/eplb"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/k8s"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/redis"
)

func StartIdleMonitor() {
	ticker := time.NewTicker(config.MONITOR_PERIOD)
	ctx := context.Background()
	var scaleUpCalls = make(map[string]int32)
	redisInterface := redis.NewRedisWrapper(redis.Rdb)
	for range ticker.C {
		now := time.Now()

		iter := redis.Rdb.Scan(ctx, 0, "last_active:*", 0).Iterator()
		for iter.Next(ctx) {
			key := iter.Val()
			funcID := key[12:]

			val, _ := redis.Rdb.Get(ctx, key).Result()
			lastTime, _ := time.Parse(time.RFC3339, val)

			if now.Sub(lastTime) > config.IDLE_TIMEOUT {
				log.Printf("[%s] IDLE timeout. Deleting K8s Resources...", funcID)
				k8s.DeleteK8sResources(funcID)

				redis.Rdb.Del(ctx, key)
				redis.Rdb.Del(ctx, fmt.Sprintf("active:%s", funcID))
				continue
			}

			// Set function stats (all active)
			lastExec := now.Add(-time.Duration(2) * time.Minute) // 0-2 minutes ago (all active)

			// eplb_redis := eplb.NewMockRedisClient()
			qps := eplb.GenerateRandomQPS(0.5, 20)
			totalRequests := int64(rand.Intn(1000) + 100)

			redis.SetFunctionStatsKey(ctx, redisInterface, funcID, map[string]string{
				"total_requests": fmt.Sprintf("%d", totalRequests),
				"last_executed":  lastExec.Format(time.RFC3339Nano),
				"total_latency":  "100.5",
				"qps":            fmt.Sprintf("%.10f", qps),
			})
			// Instead of running the scheduler (which waits for ticks),
			// directly test the workflow steps
			// 1. Collect QPS
			qpsMap := redis.CollectQPS(ctx, redisInterface, 60)
			if len(qpsMap) == 0 {
				log.Fatal("Expected non-empty QPS map")
			}
			// 2. Update EMA
			emaQpsMap := redis.UpdateEMA(ctx, redisInterface, qpsMap, 0.3)
			if len(emaQpsMap) == 0 {
				log.Fatal("Expected non-empty EMA map")
			}
			// 3. Compute plan
			plan := eplb.ComputeDesiredReplicas(emaQpsMap, 20, 1.0, true)
			if len(plan) == 0 {
				log.Fatal("Expected non-empty plan")
			}
			// 4. Simulate scale up calls
			for funcID, desiredReplicas := range plan {
				if desiredReplicas > 0 {
					eplb.ScaleUpDeployment(funcID, int32(desiredReplicas))
				}
			}
			// Verify scale up calls
			if len(scaleUpCalls) == 0 {
				log.Fatal("Expected scale up calls")
			}
			log.Fatalf("Integration test: %d functions with QPS, %d with EMA, %d in plan, %d scaled up",
				len(qpsMap), len(emaQpsMap), len(plan), len(scaleUpCalls))
			for funcID, replicas := range scaleUpCalls {
				k8s.ScaleUpDeployment(funcID, replicas)
				log.Fatalf("  %s: %d replicas", funcID, replicas)
			}
		}

	}
}
