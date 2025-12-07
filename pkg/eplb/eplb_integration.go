package eplb

// EPLB Integration with Execution Server
// Prewarm Controller 로직을 Go execution 서버에 통합
//
// 통합 방법:
// 1. eplb_go.go의 ComputeExpertAssignment() 사용
// 2. execution 서버의 기존 ScaleUpDeployment() 함수 직접 호출
// 3. funcID는 그대로 Deployment 이름으로 사용됨

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
)

// ============================================================
// Configuration
// ============================================================

const (
	DefaultEMAlpha        = 0.3
	DefaultIntervalSec   = 60
	DefaultMaxReplicas   = 20
	DefaultMinReplicas   = 0
	DefaultInactiveThreshold = 5 * 60 // 5분 (초 단위) - last_executed가 이 시간 이상 지나면 비활성
	DefaultMinQPS        = 0.01       // 최소 QPS - 이보다 낮으면 비활성으로 간주
)

// PrewarmConfig holds prewarm configuration
type PrewarmConfig struct {
	Mode          string  // "off" | "on" | "experiment"
	Alpha         float64 // EMA coefficient (used when UseAdaptiveEMA=false)
	Interval      int     // Loop interval in seconds
	Replicas      int     // Total replicas
	Aggressiveness float64 // Global aggressiveness
	
	// Adaptive EMA settings (Linear Regression 기반)
	UseAdaptiveEMA bool    // true: Linear Regression으로 α 동적 계산, false: 고정 α 사용
	MinAlpha       float64 // Adaptive EMA 최소 α (UseAdaptiveEMA=true일 때 사용)
	MaxAlpha       float64 // Adaptive EMA 최대 α (UseAdaptiveEMA=true일 때 사용)
}

// ============================================================
// Time Coefficient
// ============================================================

// GetTimeCoefficient returns time-based coefficient
func GetTimeCoefficient(hour int) float64 {
	if hour >= 0 && hour <= 5 {
		return 0.6
	} else if hour >= 6 && hour <= 8 {
		return 0.9
	} else if hour >= 9 && hour <= 18 {
		return 1.3
	} else if hour >= 19 && hour <= 22 {
		return 1.5
	} else {
		return 0.8 // 23시
	}
}

// GetCurrentTimeCoefficient returns current time coefficient
func GetCurrentTimeCoefficient() float64 {
	return GetTimeCoefficient(time.Now().Hour())
}

// ============================================================
// Compute Desired Replicas (EPLB Adapter)
// ============================================================

// ComputeDesiredReplicas computes desired replicas using EPLB
// This is the main function that replaces Python's compute_desired_replicas
func ComputeDesiredReplicas(
	emaQpsMap map[string]float64,
	numReplicas int,
	globalAggressiveness float64,
	applyTimeCoefficient bool,
) map[string]int {
	if len(emaQpsMap) == 0 {
		return make(map[string]int)
	}

	if numReplicas <= 0 {
		result := make(map[string]int)
		for funcID := range emaQpsMap {
			result[funcID] = 0
		}
		return result
	}

	// Get function IDs and compute weights
	funcIDs := make([]string, 0, len(emaQpsMap))
	weights := make([]float64, 0, len(emaQpsMap))

	kHour := 1.0
	if applyTimeCoefficient {
		kHour = GetCurrentTimeCoefficient()
	}

	for funcID, emaQps := range emaQpsMap {
		funcIDs = append(funcIDs, funcID)
		weight := math.Max(0.0, emaQps*kHour*globalAggressiveness)
		weights = append(weights, weight)
	}

	// Compute replica assignment using EPLB
	replicaCounts := ComputeExpertAssignment(weights, numReplicas)

	// Build result map with clamping
	desiredReplicas := make(map[string]int)
	for i, funcID := range funcIDs {
		count := 0
		if i < len(replicaCounts) {
			count = replicaCounts[i]
		}
		// Clamp
		if count < DefaultMinReplicas {
			count = DefaultMinReplicas
		}
		if count > DefaultMaxReplicas {
			count = DefaultMaxReplicas
		}
		desiredReplicas[funcID] = count
	}

	return desiredReplicas
}

// ============================================================
// Integration with Existing Execution Server
// ============================================================

// ScaleUpDeployment scales up a deployment for a function
// This function should be provided by the execution server
// 
// NOTE: This is a function variable that must be set by the execution server.
// In production, import from your execution server's k8s package and assign:
//   import "your-execution-server/pkg/k8s"
//   ScaleUpDeployment = k8s.ScaleUpDeployment
//
// For testing, a mock implementation is provided in eplb_integration_test.go
// which will set this variable during test initialization.
//
// This is a build-time placeholder. The actual implementation must be provided
// by the execution server that imports this package.
var ScaleUpDeployment func(funcID string, needed int32) = func(funcID string, needed int32) {
	// Default no-op implementation - should be overridden by execution server
	log.Printf("[Prewarm] ScaleUpDeployment not set - skipping scale for %s (replicas=%d)", funcID, needed)
}

// RunPrewarmScheduler runs the prewarm scheduler loop
// This should be called in a goroutine from your main function
func RunPrewarmScheduler(ctx context.Context, config PrewarmConfig, redisClient RedisClient) {
	ticker := time.NewTicker(time.Duration(config.Interval) * time.Second)
	defer ticker.Stop()

	log.Println("[Prewarm] Scheduler loop started")

	for {
		select {
		case <-ctx.Done():
			log.Println("[Prewarm] Scheduler stopped")
			return
		case <-ticker.C:
			if config.Mode == "off" {
				continue
			}

			// 1. Collect QPS from Redis
			qpsMap := CollectQPS(redisClient, config.Interval)

			// 2. Update EMA (고정 α 또는 Adaptive α)
			var emaQpsMap map[string]float64
			if config.UseAdaptiveEMA {
				// Linear Regression 기반 Adaptive EMA
				// EMA 히스토리를 기반으로 α를 동적으로 계산
				emaQpsMap = UpdateEMAAdaptive(redisClient, qpsMap, config.MinAlpha, config.MaxAlpha)
				log.Printf("[Prewarm] Using Adaptive EMA (minAlpha=%.3f, maxAlpha=%.3f)", 
					config.MinAlpha, config.MaxAlpha)
			} else {
				// 고정 α EMA
				emaQpsMap = UpdateEMA(redisClient, qpsMap, config.Alpha)
				log.Printf("[Prewarm] Using Fixed EMA (alpha=%.3f)", config.Alpha)
			}

			if len(emaQpsMap) == 0 {
				log.Println("[Prewarm] No active functions, skipping plan computation")
				continue
			}

			// 3. Compute replica plan using EPLB
			plan := ComputeDesiredReplicas(
				emaQpsMap,
				config.Replicas,
				config.Aggressiveness,
				true, // apply time coefficient
			)

			// 4. Apply plan to deployments
			// ScaleUpDeployment는 execution 서버의 기존 함수
			// funcID가 그대로 Deployment 이름으로 사용됨
			// NOTE: ScaleUpDeployment는 execution 서버에서 제공되어야 함
			// 이 파일에서는 변수로 선언되어 있고, execution 서버에서 함수를 할당해야 함
			for funcID, desiredReplicas := range plan {
				if desiredReplicas > 0 {
					// ScaleUpDeployment(funcID string, needed int32)
					// funcID는 Deployment 이름과 동일
					// 실제 execution 서버에서 이 함수를 import해서 사용
					if ScaleUpDeployment != nil {
						ScaleUpDeployment(funcID, int32(desiredReplicas))
					} else {
						log.Printf("[Prewarm] WARNING: ScaleUpDeployment not set - skipping scale for %s", funcID)
					}
					
					// 5. 계획(Desired Replicas) 저장
					planKey := fmt.Sprintf("plan:function:%s:desired_replicas", funcID)
					redisClient.SetEx(planKey, fmt.Sprintf("%d", desiredReplicas), 300)
					
					// 6. 계획 히스토리 저장 (1시간 = 60분)
					planHistoryKey := fmt.Sprintf("plan:function:%s:desired_replicas:history", funcID)
					timestamp := time.Now().Unix()
					planHistoryValue := fmt.Sprintf("%d:%d", timestamp, desiredReplicas)
					redisClient.LPush(planHistoryKey, planHistoryValue)
					redisClient.LTrim(planHistoryKey, 0, 59)
					redisClient.Expire(planHistoryKey, 4000)
				}
			}

			log.Printf("[Prewarm] Plan updated (mode=%s): %d functions", config.Mode, len(plan))
		}
	}
}

// ============================================================
// Redis Integration
// ============================================================

// RedisClient interface for Redis operations
type RedisClient interface {
	Get(key string) (string, error)
	Set(key string, value string) error
	SetEx(key string, value string, ttl int) error
	Incr(key string) (int64, error)
	Expire(key string, seconds int) error
	Scan(match string) ([]string, error)
	HGetAll(key string) (map[string]string, error) // Hash 전체 조회
	
	// List operations for time-series data (1 hour history = 60 entries)
	LPush(key string, value string) error        // 리스트 앞에 추가
	LTrim(key string, start, stop int) error    // 리스트 크기 제한 (0, 59 = 최대 60개)
	LRange(key string, start, stop int) ([]string, error) // 리스트 조회
	LLen(key string) (int64, error)            // 리스트 길이
}

// FunctionStats represents function statistics from Redis
type FunctionStats struct {
	TotalRequests int64
	LastExecuted  time.Time
	TotalLatency  float64
	QPS           float64
	IsActive      bool
}

// ============================================================
// QPS Collection (function:stats:* 방식)
// ============================================================

// CollectQPS collects QPS from Redis metrics
// Uses function:stats:* format only (Router에서 이미 qps 필드로 계산되어 저장됨)
func CollectQPS(redis RedisClient, windowSeconds int) map[string]float64 {
	qpsMap := make(map[string]float64)

	// function:stats:* format 사용 (Router에서 이미 qps 필드로 계산되어 저장됨)
	statsPattern := "function:stats:*"
	statsKeys, err := redis.Scan(statsPattern)
	if err == nil {
		for _, key := range statsKeys {
			funcID := extractFunctionIDFromStatsKey(key)
			if funcID == "" {
				continue
			}

			// Get function stats
			stats, err := getFunctionStats(redis, funcID)
			if err != nil {
				continue
			}

			// Check if function is active
			if !stats.IsActive {
				log.Printf("[Prewarm] Skipping inactive function: %s (last_executed: %v)", 
					funcID, stats.LastExecuted)
				continue
			}

			// Use QPS from Redis (Router에서 이미 계산됨)
			// Fallback to total_requests calculation if qps not available
			var qps float64
			if stats.QPS > 0 {
				// Router에서 이미 계산된 QPS 사용
				qps = stats.QPS
			} else if stats.TotalRequests > 0 {
				// Fallback: total_requests로 계산 (근사치)
				qps = float64(stats.TotalRequests) / float64(windowSeconds)
			}

			if qps >= DefaultMinQPS {
				qpsMap[funcID] = qps
			}
		}
	}

	return qpsMap
}

// getFunctionStats retrieves function statistics from Redis
func getFunctionStats(redis RedisClient, funcID string) (*FunctionStats, error) {
	statsKey := fmt.Sprintf("function:stats:%s", funcID)
	statsMap, err := redis.HGetAll(statsKey)
	if err != nil {
		return nil, err
	}

	stats := &FunctionStats{
		IsActive: false,
	}

	// Parse total_requests
	if totalStr, ok := statsMap["total_requests"]; ok {
		fmt.Sscanf(totalStr, "%d", &stats.TotalRequests)
	}

	// Parse qps (Router에서 이미 계산되어 저장됨)
	if qpsStr, ok := statsMap["qps"]; ok {
		fmt.Sscanf(qpsStr, "%f", &stats.QPS)
	}

	// Parse last_executed
	if lastExecStr, ok := statsMap["last_executed"]; ok {
		// Parse RFC3339 format: "2025-12-06T16:25:00.703605592"
		lastExec, err := time.Parse(time.RFC3339Nano, lastExecStr)
		if err != nil {
			// Try without nanoseconds
			lastExec, err = time.Parse(time.RFC3339, lastExecStr)
			if err != nil {
				// Try simplified format
				lastExec, err = time.Parse("2006-01-02T15:04:05", lastExecStr)
				if err != nil {
					log.Printf("[Prewarm] Failed to parse last_executed for %s: %v", funcID, err)
				}
			}
		}
		stats.LastExecuted = lastExec
		
		// Check if function is active (last_executed within threshold)
		now := time.Now()
		age := now.Sub(lastExec)
		stats.IsActive = age.Seconds() < float64(DefaultInactiveThreshold)
	} else {
		// No last_executed field - assume inactive
		stats.IsActive = false
	}

	// Parse total_latency
	if latencyStr, ok := statsMap["total_latency"]; ok {
		fmt.Sscanf(latencyStr, "%f", &stats.TotalLatency)
	}

	return stats, nil
}

// extractFunctionIDFromStatsKey extracts function ID from function:stats:* key
func extractFunctionIDFromStatsKey(key string) string {
	// Format: "function:stats:{funcID}"
	prefix := "function:stats:"
	if !strings.HasPrefix(key, prefix) {
		return ""
	}
	return strings.TrimPrefix(key, prefix)
}

// ============================================================
// EMA Update
// ============================================================

// UpdateEMA updates EMA QPS values
// Filters out inactive functions (QPS too low or last_executed too old)
// alpha: fixed EMA smoothing factor (0.0 ~ 1.0)
func UpdateEMA(redis RedisClient, qpsMap map[string]float64, alpha float64) map[string]float64 {
	emaQpsMap := make(map[string]float64)

	for funcID, currentQps := range qpsMap {
		// Filter 1: Skip if QPS is too low
		if currentQps < DefaultMinQPS {
			log.Printf("[Prewarm] Skipping %s: QPS too low (%.4f < %.4f)", 
				funcID, currentQps, DefaultMinQPS)
			continue
		}

		// Filter 2: Check last_executed if available
		stats, err := getFunctionStats(redis, funcID)
		if err == nil && !stats.IsActive {
			log.Printf("[Prewarm] Skipping %s: inactive (last_executed: %v, threshold: %ds)", 
				funcID, stats.LastExecuted, DefaultInactiveThreshold)
			continue
		}

		emaKey := fmt.Sprintf("state:function:%s:ema_qps", funcID)

		oldEmaStr, err := redis.Get(emaKey)
		var newEma float64

		if err == nil && oldEmaStr != "" {
			var oldEma float64
			fmt.Sscanf(oldEmaStr, "%f", &oldEma)
			// Standard EMA update: newEMA = alpha * current + (1-alpha) * oldEMA
			newEma = alpha*currentQps + (1-alpha)*oldEma
		} else {
			// First time: use current QPS as initial EMA
			newEma = currentQps
		}

		// Only update if EMA is meaningful
		if newEma >= DefaultMinQPS {
			// 1. 최신 값 저장 (빠른 조회용)
			redis.Set(emaKey, fmt.Sprintf("%f", newEma))
			
			// 2. 시계열 히스토리 저장 (1시간 = 60분) - 개별 히스토리 (하위 호환성)
			emaHistoryKey := fmt.Sprintf("state:function:%s:ema_qps:history", funcID)
			timestamp := time.Now().Unix()
			historyValue := fmt.Sprintf("%d:%.4f", timestamp, newEma) // "timestamp:value" 형식
			
			// 리스트 앞에 추가 (최신이 앞에)
			redis.LPush(emaHistoryKey, historyValue)
			
			// 최대 60개만 유지 (0~59 인덱스 = 60개)
			redis.LTrim(emaHistoryKey, 0, 59)
			
			// TTL 설정 (1시간 + 여유분 = 4000초)
			redis.Expire(emaHistoryKey, 4000)
			
			emaQpsMap[funcID] = newEma
		} else {
			log.Printf("[Prewarm] Skipping %s: EMA too low (%.4f < %.4f)", 
				funcID, newEma, DefaultMinQPS)
		}
	}

	return emaQpsMap
}

// UpdateEMAAdaptive updates EMA QPS values with adaptive alpha using linear regression
// Filters out inactive functions (QPS too low or last_executed too old)
// Uses history to compute adaptive alpha based on QPS trend
// minAlpha, maxAlpha: alpha의 최소/최대값 (예: 0.1, 0.5)
func UpdateEMAAdaptive(redis RedisClient, qpsMap map[string]float64, minAlpha, maxAlpha float64) map[string]float64 {
	emaQpsMap := make(map[string]float64)

	for funcID, currentQps := range qpsMap {
		// Filter 1: Check QPS threshold
		if currentQps < DefaultMinQPS {
			log.Printf("[Prewarm] Skipping %s: QPS too low (%.4f < %.4f)", 
				funcID, currentQps, DefaultMinQPS)
			continue
		}

		// Filter 2: Check last_executed if available
		stats, err := getFunctionStats(redis, funcID)
		if err == nil && !stats.IsActive {
			log.Printf("[Prewarm] Skipping %s: inactive (last_executed: %v, threshold: %ds)", 
				funcID, stats.LastExecuted, DefaultInactiveThreshold)
			continue
		}

		emaKey := fmt.Sprintf("state:function:%s:ema_qps", funcID)

		// Get recent history (last 10 minutes for adaptive alpha calculation)
		historyEntries, err := GetEMAHistory(redis, funcID, 10)
		history := make([]float64, 0, len(historyEntries))
		for _, entry := range historyEntries {
			history = append(history, entry.EMA)
		}

		// Get current EMA (if exists)
		oldEmaStr, err := redis.Get(emaKey)
		var oldEMA float64
		if err == nil && oldEmaStr != "" {
			fmt.Sscanf(oldEmaStr, "%f", &oldEMA)
			// Add old EMA to history if not already there
			if len(history) == 0 || (len(history) > 0 && history[0] != oldEMA) {
				history = append([]float64{oldEMA}, history...)
			}
		}

		// Compute adaptive alpha using linear regression
		// History를 시간 순서대로 정렬 (오래된 것부터)
		reversedHistory := make([]float64, len(history))
		for i := 0; i < len(history); i++ {
			reversedHistory[i] = history[len(history)-1-i]
		}
		fullHistory := append(reversedHistory, currentQps)
		
		adaptiveAlpha := ComputeAdaptiveAlpha(fullHistory, minAlpha, maxAlpha)

		// Update EMA with adaptive alpha
		var newEMA float64
		if len(history) > 0 || oldEmaStr != "" {
			if oldEmaStr == "" {
				oldEMA = currentQps
			}
			newEMA = adaptiveAlpha*currentQps + (1-adaptiveAlpha)*oldEMA
		} else {
			newEMA = currentQps
		}

		// Only update if EMA is meaningful
		if newEMA >= DefaultMinQPS {
			// 1. 최신 값 저장 (빠른 조회용)
			redis.Set(emaKey, fmt.Sprintf("%f", newEMA))
			
			// 2. 시계열 히스토리 저장 (1시간 = 60분)
			emaHistoryKey := fmt.Sprintf("state:function:%s:ema_qps:history", funcID)
			timestamp := time.Now().Unix()
			historyValue := fmt.Sprintf("%d:%.4f", timestamp, newEMA)
			
			redis.LPush(emaHistoryKey, historyValue)
			redis.LTrim(emaHistoryKey, 0, 59)
			redis.Expire(emaHistoryKey, 4000)
			
			emaQpsMap[funcID] = newEMA
		} else {
			log.Printf("[Prewarm] Skipping %s: EMA too low (%.4f < %.4f)", 
				funcID, newEMA, DefaultMinQPS)
		}
	}

	return emaQpsMap
}

// ============================================================
// History Retrieval Functions (for other clients)
// ============================================================

// EMAHistoryEntry represents a single EMA history entry
type EMAHistoryEntry struct {
	Timestamp time.Time
	EMA       float64
}

// PlanHistoryEntry represents a single plan history entry
type PlanHistoryEntry struct {
	Timestamp time.Time
	Replicas  int
}

// GetEMAHistory retrieves EMA QPS history for a function (last N minutes)
// Returns up to 60 entries (1 hour) sorted by time (newest first)
func GetEMAHistory(redis RedisClient, funcID string, maxMinutes int) ([]EMAHistoryEntry, error) {
	if maxMinutes > 60 {
		maxMinutes = 60 // 최대 60분
	}
	if maxMinutes <= 0 {
		maxMinutes = 60 // 기본값
	}

	historyKey := fmt.Sprintf("state:function:%s:ema_qps:history", funcID)
	
	// 최근 N개 조회 (0부터 maxMinutes-1까지)
	values, err := redis.LRange(historyKey, 0, maxMinutes-1)
	if err != nil {
		return nil, err
	}

	entries := make([]EMAHistoryEntry, 0, len(values))
	for _, value := range values {
		// 형식: "timestamp:ema_value"
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			continue
		}

		timestamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		ema, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			continue
		}

		entries = append(entries, EMAHistoryEntry{
			Timestamp: time.Unix(timestamp, 0),
			EMA:       ema,
		})
	}

	return entries, nil
}

// GetPlanHistory retrieves replica plan history for a function (last N minutes)
// Returns up to 60 entries (1 hour) sorted by time (newest first)
func GetPlanHistory(redis RedisClient, funcID string, maxMinutes int) ([]PlanHistoryEntry, error) {
	if maxMinutes > 60 {
		maxMinutes = 60 // 최대 60분
	}
	if maxMinutes <= 0 {
		maxMinutes = 60 // 기본값
	}

	historyKey := fmt.Sprintf("plan:function:%s:desired_replicas:history", funcID)
	
	// 최근 N개 조회 (0부터 maxMinutes-1까지)
	values, err := redis.LRange(historyKey, 0, maxMinutes-1)
	if err != nil {
		return nil, err
	}

	entries := make([]PlanHistoryEntry, 0, len(values))
	for _, value := range values {
		// 형식: "timestamp:replicas"
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			continue
		}

		timestamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		replicas, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}

		entries = append(entries, PlanHistoryEntry{
			Timestamp: time.Unix(timestamp, 0),
			Replicas:  replicas,
		})
	}

	return entries, nil
}

// GetCurrentEMA retrieves current EMA QPS value for a function
func GetCurrentEMA(redis RedisClient, funcID string) (float64, error) {
	emaKey := fmt.Sprintf("state:function:%s:ema_qps", funcID)
	value, err := redis.Get(emaKey)
	if err != nil {
		return 0, err
	}

	var ema float64
	_, err = fmt.Sscanf(value, "%f", &ema)
	return ema, err
}

// GetCurrentPlan retrieves current replica plan for a function
func GetCurrentPlan(redis RedisClient, funcID string) (int, error) {
	planKey := fmt.Sprintf("plan:function:%s:desired_replicas", funcID)
	value, err := redis.Get(planKey)
	if err != nil {
		return 0, err
	}

	var replicas int
	_, err = fmt.Sscanf(value, "%d", &replicas)
	return replicas, err
}

// GetAllFunctionsWithHistory returns list of function IDs that have history data
func GetAllFunctionsWithHistory(redis RedisClient) ([]string, error) {
	// EMA 히스토리 키 패턴으로 스캔
	pattern := "state:function:*:ema_qps:history"
	keys, err := redis.Scan(pattern)
	if err != nil {
		return nil, err
	}

	funcIDs := make([]string, 0, len(keys))
	for _, key := range keys {
		// "state:function:{func_id}:ema_qps:history" -> func_id 추출
		parts := strings.Split(key, ":")
		if len(parts) >= 3 {
			funcID := parts[2]
			funcIDs = append(funcIDs, funcID)
		}
	}

	return funcIDs, nil
}

// ============================================================
// Current Replica Storage (placeholder for execution server)
// ============================================================

// SaveCurrentReplicas saves current replica count for a function
// This should be called by the execution server when replicas change
func SaveCurrentReplicas(redis RedisClient, funcID string, currentReplicas int) error {
	// 최신 값 저장
	currentKey := fmt.Sprintf("state:function:%s:current_replicas", funcID)
	redis.Set(currentKey, fmt.Sprintf("%d", currentReplicas))
	
	// 히스토리 저장 (1시간 = 60분)
	historyKey := fmt.Sprintf("state:function:%s:current_replicas:history", funcID)
	timestamp := time.Now().Unix()
	historyValue := fmt.Sprintf("%d:%d", timestamp, currentReplicas)
	
	redis.LPush(historyKey, historyValue)
	redis.LTrim(historyKey, 0, 59)
	redis.Expire(historyKey, 4000)
	
	return nil
}

// GetCurrentReplicasHistory retrieves current replica history for a function
func GetCurrentReplicasHistory(redis RedisClient, funcID string, maxMinutes int) ([]PlanHistoryEntry, error) {
	if maxMinutes > 60 {
		maxMinutes = 60
	}
	if maxMinutes <= 0 {
		maxMinutes = 60
	}

	historyKey := fmt.Sprintf("state:function:%s:current_replicas:history", funcID)
	values, err := redis.LRange(historyKey, 0, maxMinutes-1)
	if err != nil {
		return nil, err
	}

	entries := make([]PlanHistoryEntry, 0, len(values))
	for _, value := range values {
		parts := strings.Split(value, ":")
		if len(parts) != 2 {
			continue
		}

		timestamp, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			continue
		}

		replicas, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}

		entries = append(entries, PlanHistoryEntry{
			Timestamp: time.Unix(timestamp, 0),
			Replicas:  replicas,
		})
	}

	return entries, nil
}

// GetCurrentReplicas retrieves current replica count for a function
// This should be implemented by the execution server to fetch from Kubernetes
func GetCurrentReplicas(funcID string) (int, error) {
	// TODO: Implement in execution server
	// This should query Kubernetes API to get actual replica count
	// Example:
	//   deploy, err := k8sClient.AppsV1().Deployments(namespace).Get(context.TODO(), funcID, metav1.GetOptions{})
	//   if err != nil {
	//       return 0, err
	//   }
	//   return int(*deploy.Spec.Replicas), nil
	return 0, fmt.Errorf("GetCurrentReplicas not implemented - should be provided by execution server")
}
