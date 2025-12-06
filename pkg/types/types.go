package types

type FunctionMetadata struct {
	Name    string
	Code    string
	Type    string
	Request string
}

type FunctionRequest struct {
	Name    string `json:"name"`
	Code    string `json:"code"`
	Type    string `json:"type"`
	Request string `json:"request"`
}

// ExecutionRequest is the message received from the function:execution:queue
type ExecutionRequest struct {
	RequestID  string                 `json:"requestId"`
	FunctionID string                 `json:"functionId"`
	Args       map[string]interface{} `json:"args"`
}

// ExecutionResult is the message published to the result:<requestId> channel
type ExecutionResult struct {
	Status        string  `json:"status"`
	ExecutionType string  `json:"executionType"` // "cold" or "warm"
	Duration      float64 `json:"duration"`      // in seconds
	Logs          string  `json:"logs"`
	Result        string  `json:"result"`        // JSON string
}
