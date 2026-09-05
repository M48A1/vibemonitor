package protocol

import (
	"encoding/json"
	"time"
)

const (
	JSONRPCVersion        = "2.0"
	MethodAgentReport     = "agent.report"
	MethodAgentBasicInfo  = "agent.basicInfo"
	MethodAgentPingResult = "agent.pingResult"
	MethodAgentPull       = "agent.pull"
)

// Request is a standard JSON-RPC 2.0 request
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id,omitempty"`
}

// Response is a standard JSON-RPC 2.0 response
type Response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *RPCError `json:"error,omitempty"`
}

// RPCError represents a JSON-RPC error
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// BasicInfoParams wraps agent.basicInfo parameters
type BasicInfoParams struct {
	Info BasicInfo `json:"info"`
}

// BasicInfo describes static hardware and OS information of a client node
type BasicInfo struct {
	ReportIntervalSeconds float64 `json:"report_interval_seconds,omitempty"`
	CPUName               string  `json:"cpu_name"`
	CPUCores              int     `json:"cpu_cores"`
	CPUPhysicalCores      int     `json:"cpu_physical_cores"`
	Arch                  string  `json:"arch"`
	OS                    string  `json:"os"`
	KernelVersion         string  `json:"kernel_version"`
	IPv4                  string  `json:"ipv4"`
	IPv6                  string  `json:"ipv6"`
	MemTotal              int64   `json:"mem_total"`
	SwapTotal             int64   `json:"swap_total"`
	DiskTotal             int64   `json:"disk_total"`
	GPUName               string  `json:"gpu_name"`
	Virtualization        string  `json:"virtualization"`
	Version               string  `json:"version"`
}

// ReportParams wraps agent.report parameters
type ReportParams struct {
	Report      Report   `json:"report"`
	AckEventIDs []string `json:"ack_event_ids,omitempty"`
}

// PingTarget defines a target for latency testing
type PingTarget struct {
	Name string `json:"name"` // e.g. "电信", "联通", "香港", "Google"
	Host string `json:"host"` // IP or host:port
}

// PingResult records latency measurement for a target
type PingResult struct {
	Method  string `json:"method,omitempty"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	Latency int    `json:"latency"` // ms, -1 if unreachable/timed out
}

// Report contains dynamic, real-time performance metrics
type Report struct {
	ReportIntervalSeconds float64           `json:"report_interval_seconds,omitempty"`
	UUID                  string            `json:"uuid,omitempty"`
	CPU                   CPUReport         `json:"cpu"`
	RAM                   RAMReport         `json:"ram"`
	Swap                  RAMReport         `json:"swap"`
	Load                  LoadReport        `json:"load"`
	Disk                  DiskReport        `json:"disk"`
	Network               NetworkReport     `json:"network"`
	Connections           ConnectionsReport `json:"connections"`
	PingResults           []PingResult      `json:"ping_results,omitempty"`
	Uptime                int64             `json:"uptime"`
	Process               int               `json:"process"`
	Message               string            `json:"message,omitempty"`
	UpdatedAt             time.Time         `json:"updated_at"`
}

type CPUReport struct {
	Name  string  `json:"name,omitempty"`
	Cores int     `json:"cores,omitempty"`
	Arch  string  `json:"arch,omitempty"`
	Usage float64 `json:"usage"` // e.g. 12.5%
}

type RAMReport struct {
	Total int64 `json:"total"` // Bytes
	Used  int64 `json:"used"`  // Bytes
}

type LoadReport struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
}

type DiskReport struct {
	Total int64 `json:"total"` // Bytes
	Used  int64 `json:"used"`  // Bytes
}

type NetworkReport struct {
	Up        int64 `json:"up"`        // Bytes/s
	Down      int64 `json:"down"`      // Bytes/s
	TotalUp   int64 `json:"totalUp"`   // Cumulative Bytes
	TotalDown int64 `json:"totalDown"` // Cumulative Bytes
}

type ConnectionsReport struct {
	TCP int `json:"tcp"`
	UDP int `json:"udp"`
}

// Helper to construct success JSON-RPC response
func SuccessResponse(id any, result any) Response {
	return Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Result:  result,
	}
}

// Helper to construct error JSON-RPC response
func ErrorResponse(id any, code int, message string, data any) Response {
	return Response{
		JSONRPC: JSONRPCVersion,
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
}
