package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/k8s"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/types"
	"github.com/redis/go-redis/v9"
)

const FunctionExecutionQueue = "function:execution:queue"

func UpdateLastActivity(funcID string) {
	ctx := context.Background()
	key := fmt.Sprintf("last_active:%s", funcID)
	Rdb.Set(ctx, key, time.Now().Format(time.RFC3339), 0)
}

func GetLastActivity(funcID string) time.Time {
	ctx := context.Background()
	val, err := Rdb.Get(ctx, fmt.Sprintf("last_active:%s", funcID)).Result()
	if err != nil {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, val)
	return t
}

func SaveFunctionToRedis(req types.FunctionRequest, funcID string) error {
	meta := types.FunctionMetadata{Name: funcID, Code: req.Code, Type: req.Type, Request: req.Request}
	data, _ := json.Marshal(meta)

	UpdateLastActivity(funcID)
	return Rdb.Set(context.Background(), fmt.Sprintf("func:%s", funcID), data, 0).Err()
}

func GetFunctionFromRedis(funcID string) (*types.FunctionMetadata, error) {
	val, err := Rdb.Get(context.Background(), fmt.Sprintf("func:%s", funcID)).Result()
	if err != nil {
		return nil, err
	}
	var meta types.FunctionMetadata
	json.Unmarshal([]byte(val), &meta)
	return &meta, nil
}

func IncRequestAndScale(funcID string) {
	ctx := context.Background()
	key := fmt.Sprintf("active:%s", funcID)
	currentActive, _ := Rdb.Incr(ctx, key).Result()
	Rdb.Expire(ctx, key, 1*time.Hour)
	neededReplicas := int32(math.Ceil(float64(currentActive) / float64(config.CONCURRENCY_PER_POD)))
	if neededReplicas > 1 {
		go k8s.ScaleUpDeployment(funcID, neededReplicas)
	}
}

func DecRequest(funcID string) {
	Rdb.Decr(context.Background(), fmt.Sprintf("active:%s", funcID))
}

func FetchExecutionRequest(ctx context.Context) (*types.ExecutionRequest, error) {
	result, err := Rdb.BRPop(ctx, 0, FunctionExecutionQueue).Result()
	if err != nil {
		return nil, err
	}

	// result is a []string with the first element being the key and the second the value
	if len(result) != 2 {
		return nil, fmt.Errorf("invalid result from BRPop: %v", result)
	}

	var req types.ExecutionRequest
	if err := json.Unmarshal([]byte(result[1]), &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal request: %w", err)
	}

	return &req, nil
}

func PublishExecutionResult(ctx context.Context, requestID string, result *types.ExecutionResult) error {
	channel := fmt.Sprintf("result:%s", requestID)
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal result: %w", err)
	}

	return Rdb.Publish(ctx, channel, payload).Err()
}

func SubscribeToResult(ctx context.Context, requestID string) *redis.PubSub {
	channel := fmt.Sprintf("result:%s", requestID)
	return Rdb.Subscribe(ctx, channel)
}
