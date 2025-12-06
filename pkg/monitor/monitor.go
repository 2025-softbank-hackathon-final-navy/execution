package monitor

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/k8s"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/redis"
)

func StartIdleMonitor() {
	ticker := time.NewTicker(config.MONITOR_PERIOD)
	ctx := context.Background()

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
			}
		}
	}
}
