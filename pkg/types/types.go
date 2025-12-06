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
