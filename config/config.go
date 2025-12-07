package config

import (
	"os"
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
	MAX_REPLICAS        = 30
)

// AppConfig holds the application configuration, loaded from environment variables.
var AppConfig struct {
	S3Bucket  string
	AWSRegion string
}

var (
	ValidNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

// Load populates the AppConfig from environment variables.
func Load() {
	AppConfig.S3Bucket = getEnv("S3_BUCKET_NAME", "serverless-bistro-code-b58969e9")
	AppConfig.AWSRegion = getEnv("AWS_REGION", "ap-northeast-2")
}

// getEnv retrieves an environment variable or returns a fallback value.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}
