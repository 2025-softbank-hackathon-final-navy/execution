package config

import (
	"regexp"
	"time"
)

const (
	IDLE_TIMEOUT        = 15 * time.Minute
	MONITOR_PERIOD      = 1 * time.Minute
	RUNNER_IMAGE        = "localhost/py-runner:latest"
	NAMESPACE           = "default"
	REDIS_ADDR          = "redis-service:6379"
	CONCURRENCY_PER_POD = 5
	MAX_REPLICAS        = 10
)

var (
	ValidNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)
