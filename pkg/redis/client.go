package redis

import (
	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/redis/go-redis/v9"
)

var Rdb *redis.Client

func InitRedisClient() {
	Rdb = redis.NewClient(&redis.Options{
		Addr: config.REDIS_ADDR,
		DB:   0,
	})
}
