package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/k8s"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/redis"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/storage"
	"github.com/2025-softbank-hackathon-final-navy/execution/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// StartWorker initializes and starts the Redis queue worker.
func StartWorker() {
	log.Println("Starting Redis queue worker...")
	for {
		ctx := context.Background()
		req, err := redis.FetchExecutionRequest(ctx)
		if err != nil {
			log.Printf("Error fetching execution request from Redis: %v", err)
			time.Sleep(5 * time.Second) // Wait before retrying
			continue
		}
		if req == nil {
			// This should ideally not happen with BRPop, but as a safeguard
			time.Sleep(1 * time.Second)
			continue
		}

		go processRequest(ctx, req)
	}
}

// processRequest handles a single function execution request.
func processRequest(ctx context.Context, req *types.ExecutionRequest) {
	log.Printf("Processing request %s for function %s", req.RequestID, req.FunctionID)
	startTime := time.Now()
	executionType := "cold" // Assume cold start initially

	// 1. Get function code using cache-aside pattern
	meta, err := redis.GetFunctionFromRedis(req.FunctionID)
	if err != nil {
		log.Printf("Cache miss for function %s: %v. Fetching from S3...", req.FunctionID, err)
		code, err := storage.DownloadCode(req.FunctionID, req.Runtime)
		if err != nil {
			log.Printf("Failed to download code for function %s from S3: %v", req.FunctionID, err)
			publishErrorResult(ctx, req.RequestID, req.FunctionID, err, "unknown")
			return
		}
		log.Printf("Successfully downloaded code for function %s from S3. Populating cache.", req.FunctionID)
		functionReqForCache := types.FunctionRequest{Name: req.FunctionID, Code: code, Type: req.Runtime, Request: ""}
		if err := redis.SaveFunctionToRedis(functionReqForCache, req.FunctionID); err != nil {
			log.Printf("Failed to populate cache for function %s: %v", req.FunctionID, err)
			// Continue execution even if caching fails
		}
		meta = &types.FunctionMetadata{Name: req.FunctionID, Code: code, Type: req.Runtime, Request: ""}
	} else {
		log.Printf("Cache hit for function %s", req.FunctionID)
	}

	// 2. Ensure K8s resources are ready
	_, err = k8s.Clientset.CoreV1().Services(config.NAMESPACE).Get(ctx, req.FunctionID, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		functionReq := types.FunctionRequest{Name: meta.Name, Code: meta.Code, Type: meta.Type, Request: meta.Request}
		if err := k8s.CreateK8sResources(req.FunctionID, functionReq, req.UseGPU); err != nil {
			log.Printf("Error creating K8s resources for function %s: %v", req.FunctionID, err)
			publishErrorResult(ctx, req.RequestID, req.FunctionID, err, executionType)
			return
		}
		if !k8s.WaitForPodReady(req.FunctionID) {
			log.Printf("Timeout waiting for pod %s to be ready", req.FunctionID)
			publishErrorResult(ctx, req.RequestID, req.FunctionID, fmt.Errorf("timeout waiting for pod to be ready"), executionType)
			return
		}
	} else if err != nil {
		log.Printf("Error checking K8s service for function %s: %v", req.FunctionID, err)
		publishErrorResult(ctx, req.RequestID, req.FunctionID, err, executionType)
		return
	} else {
		executionType = "warm" // Service already exists, likely a warm start
	}

	// Update last activity for scaling/idle monitoring
	redis.UpdateLastActivity(req.FunctionID)
	redis.IncRequestAndScale(req.FunctionID)
	defer redis.DecRequest(req.FunctionID)

	// 3. Proxy request to the function pod
	targetURL := fmt.Sprintf("http://%s.%s.svc.cluster.local:8080", req.FunctionID, config.NAMESPACE)
	result, logs, err := callFunctionPod(ctx, targetURL, req.Args)
	if err != nil {
		log.Printf("Error calling function pod %s: %v", req.FunctionID, err)
		publishErrorResult(ctx, req.RequestID, req.FunctionID, err, executionType)
		return
	}

	duration := time.Since(startTime).Seconds()
	executionResult := &types.ExecutionResult{
		Status:        "success",
		ExecutionType: executionType,
		Duration:      duration,
		Logs:          logs,
		Result:        result,
	}

	if err := redis.PublishExecutionResult(ctx, req.RequestID, executionResult); err != nil {
		log.Printf("Error publishing result for request %s: %v", req.RequestID, err)
	}
	log.Printf("Finished processing request %s for function %s in %.2f seconds", req.RequestID, req.FunctionID, duration)
}

// callFunctionPod makes an HTTP POST request to the function pod with the given arguments.
func callFunctionPod(ctx context.Context, url string, args map[string]interface{}) (string, string, error) {
	jsonArgs, err := json.Marshal(args)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal arguments: %w", err)
	}

	client := &http.Client{Timeout: 30 * time.Second} // Configurable timeout
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonArgs))
	if err != nil {
		return "", "", fmt.Errorf("failed to create HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(httpReq)
	if err != nil {
		return "", "", fmt.Errorf("failed to send request to function pod: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", string(bodyBytes), fmt.Errorf("function pod returned non-200 status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	// Assuming logs are part of the response body or can be extracted.
	// For now, we'll just return the body as result and an empty string for logs.
	// A more sophisticated system might parse the response for separate log fields.
	return string(bodyBytes), "Function execution logs (placeholder)", nil
}

// publishErrorResult is a helper to publish an error result to Redis.
func publishErrorResult(ctx context.Context, requestID, funcID string, err error, currentExecutionType string) {
	errorExecutionType := currentExecutionType + "_fail"
	if currentExecutionType == "cold" {
		errorExecutionType = "cold_fail"
	} else if currentExecutionType == "warm" {
		errorExecutionType = "warm_fail"
	} else {
		errorExecutionType = "unknown_fail"
	}

	executionResult := &types.ExecutionResult{
		Status:        "error",
		ExecutionType: errorExecutionType,
		Duration:      0,
		Logs:          fmt.Sprintf("Execution failed for function %s: %v", funcID, err),
		Result:        fmt.Sprintf("{\"error\": \"%v\"}", err),
	}
	if pubErr := redis.PublishExecutionResult(ctx, requestID, executionResult); pubErr != nil {
		log.Printf("Critical: Failed to publish error result for request %s: %v", requestID, pubErr)
	}
}
