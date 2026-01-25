package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
)

// LogEntry represents a single audit log entry with blockchain-like chaining
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Endpoint  string `json:"endpoint"`
	Request   struct {
		Body string `json:"body"`
	} `json:"request"`
	Response struct {
		Body       string `json:"body"`
		StatusCode int    `json:"status_code"`
		Error      string `json:"error,omitempty"`
		IsComplete bool   `json:"is_complete"`
	} `json:"response"`
	// Trace field is now part of the integrity check
	Trace    *TraceContext `json:"trace,omitempty"`
	PrevHash string        `json:"prev_hash"`
	Hash     string        `json:"hash"`
}

// TraceContext represents distributed tracing metadata
type TraceContext struct {
	TraceID      string          `json:"trace_id,omitempty"`
	SpanID       string          `json:"span_id,omitempty"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	SpanType     string          `json:"span_type,omitempty"`
	SpanName     string          `json:"span_name,omitempty"`
	ToolCall     *ToolCallInfo   `json:"tool_call,omitempty"`
	ToolResult   *ToolResultInfo `json:"tool_result,omitempty"`
}

// ToolCallInfo represents a tool invocation
type ToolCallInfo struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function invocation
type FunctionCall struct {
	Name          string `json:"name"`
	ArgumentsHash string `json:"arguments_hash"`
}

// ToolResultInfo represents the result of a tool execution
type ToolResultInfo struct {
	ToolCallID  string `json:"tool_call_id"`
	ContentHash string `json:"content_hash"`
	IsError     bool   `json:"is_error,omitempty"`
}

// Exit codes
const (
	ExitSuccess      = 0
	ExitFileError    = 1
	ExitChainBroken  = 2
	ExitDataTampered = 3
	ExitParseError   = 4
	ExitScanError    = 5
)

var (
	logFile = flag.String("file", "logs/audit.jsonl", "Path to the audit log file")
	verbose = flag.Bool("verbose", false, "Enable verbose output for each line")
	quiet   = flag.Bool("quiet", false, "Suppress all output except errors")
)

func main() {
	flag.Parse()

	if err := verifyLog(*logFile); err != nil {
		log.Fatal(err)
	}
}

func verifyLog(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening log file: %v\n", err)
		os.Exit(ExitFileError)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	// Set maximum buffer size for large log entries (default is 64KB)
	const maxScanTokenSize = 1024 * 1024 // 1MB
	buf := make([]byte, maxScanTokenSize)
	scanner.Buffer(buf, maxScanTokenSize)

	var expectedPrevHash string
	lineNum := 0
	errorCount := 0

	for scanner.Scan() {
		lineNum++
		var entry LogEntry

		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			errorCount++
			fmt.Fprintf(os.Stderr, "Parse error on line %d: %v\n", lineNum, err)
			if errorCount > 10 {
				fmt.Fprintf(os.Stderr, "Too many parse errors, aborting verification\n")
				os.Exit(ExitParseError)
			}
			continue
		}

		// Verify chain continuity (skip for first entry)
		if expectedPrevHash != "" && entry.PrevHash != expectedPrevHash {
			fmt.Fprintf(os.Stderr, "❌ CHAIN BROKEN at line %d!\n", lineNum)
			fmt.Fprintf(os.Stderr, "   Expected prev_hash: %s...\n", expectedPrevHash[:16])
			fmt.Fprintf(os.Stderr, "   Found prev_hash:    %s...\n", entry.PrevHash[:16])
			os.Exit(ExitChainBroken)
		}

		// Recalculate hash for current entry
		calculatedHash := calculateHash(&entry)

		if calculatedHash != entry.Hash {
			fmt.Fprintf(os.Stderr, "❌ DATA TAMPERED at line %d!\n", lineNum)
			fmt.Fprintf(os.Stderr, "   Expected hash: %s\n", calculatedHash)
			fmt.Fprintf(os.Stderr, "   Found hash:    %s\n", entry.Hash)
			os.Exit(ExitDataTampered)
		}

		expectedPrevHash = entry.Hash

		if *verbose && !*quiet {
			fmt.Printf("✅ Line %d verified (hash: %s...)\n", lineNum, entry.Hash[:16])
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading log file: %v\n", err)
		os.Exit(ExitScanError)
	}

	if lineNum == 0 {
		fmt.Fprintf(os.Stderr, "Warning: Log file is empty\n")
	}

	if !*quiet {
		fmt.Printf("\n✅ Verification successful!\n")
		fmt.Printf("   Total entries verified: %d\n", lineNum)
		fmt.Printf("   Chain integrity: INTACT\n")
		fmt.Printf("   Data integrity: VERIFIED\n")
	}

	os.Exit(ExitSuccess)
	return nil
}

// calculateHash computes the SHA-256 hash of a log entry
// Must match the calculation in internal/audit/worker.go exactly
func calculateHash(entry *LogEntry) string {
	h := sha256.New()

	// Write all components in the exact order as worker.go
	h.Write([]byte(entry.Timestamp))
	h.Write([]byte(entry.Endpoint))
	h.Write([]byte(entry.Request.Body))
	h.Write([]byte(entry.Response.Body))
	fmt.Fprintf(h, "%d", entry.Response.StatusCode)
	h.Write([]byte(entry.Response.Error))
	if entry.Response.IsComplete {
		h.Write([]byte("true"))
	} else {
		h.Write([]byte("false"))
	}

	// Include trace context if present (maintains backward compatibility)
	if entry.Trace != nil {
		h.Write([]byte(entry.Trace.TraceID))
		h.Write([]byte(entry.Trace.SpanID))
		h.Write([]byte(entry.Trace.ParentSpanID))
		h.Write([]byte(entry.Trace.SpanType))
		h.Write([]byte(entry.Trace.SpanName))

		// Include tool call details if present
		if entry.Trace.ToolCall != nil {
			h.Write([]byte(entry.Trace.ToolCall.ID))
			h.Write([]byte(entry.Trace.ToolCall.Type))
			h.Write([]byte(entry.Trace.ToolCall.Function.Name))
			h.Write([]byte(entry.Trace.ToolCall.Function.ArgumentsHash))
		}

		// Include tool result details if present
		if entry.Trace.ToolResult != nil {
			h.Write([]byte(entry.Trace.ToolResult.ToolCallID))
			h.Write([]byte(entry.Trace.ToolResult.ContentHash))
			if entry.Trace.ToolResult.IsError {
				h.Write([]byte("true"))
			} else {
				h.Write([]byte("false"))
			}
		}
	}

	h.Write([]byte(entry.PrevHash))
	return hex.EncodeToString(h.Sum(nil))
}
