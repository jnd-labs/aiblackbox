package config

import (
	"os"
	"testing"

	"github.com/spf13/viper"
)

// TestBackwardCompatibility verifies that configs without streaming section still work
func TestBackwardCompatibility(t *testing.T) {
	// Create a temporary config file without streaming section
	oldConfigContent := `
server:
  port: 8080
  genesis_seed: "test-seed"

endpoints:
  - name: "test"
    target: "http://localhost:8000"

storage:
  path: "/tmp/test_audit.jsonl"
`
	tmpFile, err := os.CreateTemp("", "old_config_*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(oldConfigContent)); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	tmpFile.Close()

	// Load the config
	v := viper.New()
	v.SetConfigFile(tmpFile.Name())

	// Set defaults (same as Load() function)
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.genesis_seed", "aiblackbox-default-seed")
	v.SetDefault("storage.path", "./logs/audit.jsonl")
	v.SetDefault("streaming.max_audit_body_size", 10485760)
	v.SetDefault("streaming.stream_timeout", 300)
	v.SetDefault("streaming.enable_sequence_tracking", true)

	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	// Validate the config
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Config validation failed: %v", err)
	}

	// Verify defaults were applied
	if cfg.Streaming.MaxAuditBodySize != 10485760 {
		t.Errorf("Expected MaxAuditBodySize to be 10485760, got %d", cfg.Streaming.MaxAuditBodySize)
	}

	if cfg.Streaming.StreamTimeout != 300 {
		t.Errorf("Expected StreamTimeout to be 300, got %d", cfg.Streaming.StreamTimeout)
	}

	if !cfg.Streaming.EnableSequenceTracking {
		t.Error("Expected EnableSequenceTracking to be true")
	}

	t.Log("✓ Backward compatibility verified: old config format works with defaults")
}

// TestStreamingConfigExplicitValues verifies that explicit streaming values are respected
func TestStreamingConfigExplicitValues(t *testing.T) {
	// Create a config file with explicit streaming values
	newConfigContent := `
server:
  port: 8080
  genesis_seed: "test-seed"

endpoints:
  - name: "test"
    target: "http://localhost:8000"

storage:
  path: "/tmp/test_audit.jsonl"

streaming:
  max_audit_body_size: 20971520
  stream_timeout: 600
  enable_sequence_tracking: false
`
	tmpFile, err := os.CreateTemp("", "new_config_*.yaml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write([]byte(newConfigContent)); err != nil {
		t.Fatalf("Failed to write temp file: %v", err)
	}
	tmpFile.Close()

	// Load the config
	v := viper.New()
	v.SetConfigFile(tmpFile.Name())

	// Set defaults
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.genesis_seed", "aiblackbox-default-seed")
	v.SetDefault("storage.path", "./logs/audit.jsonl")
	v.SetDefault("streaming.max_audit_body_size", 10485760)
	v.SetDefault("streaming.stream_timeout", 300)
	v.SetDefault("streaming.enable_sequence_tracking", true)

	if err := v.ReadInConfig(); err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	// Validate the config
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Config validation failed: %v", err)
	}

	// Verify explicit values were used (not defaults)
	if cfg.Streaming.MaxAuditBodySize != 20971520 {
		t.Errorf("Expected MaxAuditBodySize to be 20971520, got %d", cfg.Streaming.MaxAuditBodySize)
	}

	if cfg.Streaming.StreamTimeout != 600 {
		t.Errorf("Expected StreamTimeout to be 600, got %d", cfg.Streaming.StreamTimeout)
	}

	if cfg.Streaming.EnableSequenceTracking {
		t.Error("Expected EnableSequenceTracking to be false")
	}

	t.Log("✓ Explicit streaming config values are correctly loaded")
}

// TestStreamingConfigValidation verifies that invalid values are rejected
func TestStreamingConfigValidation(t *testing.T) {
	tests := []struct {
		name          string
		maxBodySize   int64
		streamTimeout int
		expectError   bool
		errorContains string
	}{
		{
			name:          "valid config",
			maxBodySize:   10485760,
			streamTimeout: 300,
			expectError:   false,
		},
		{
			name:          "negative max body size",
			maxBodySize:   -1,
			streamTimeout: 300,
			expectError:   true,
			errorContains: "max_audit_body_size must be positive",
		},
		{
			name:          "zero max body size",
			maxBodySize:   0,
			streamTimeout: 300,
			expectError:   true,
			errorContains: "max_audit_body_size must be positive",
		},
		{
			name:          "negative stream timeout",
			maxBodySize:   10485760,
			streamTimeout: -1,
			expectError:   true,
			errorContains: "stream_timeout must be positive",
		},
		{
			name:          "zero stream timeout",
			maxBodySize:   10485760,
			streamTimeout: 0,
			expectError:   true,
			errorContains: "stream_timeout must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Server: ServerConfig{
					Port:        8080,
					GenesisSeed: "test",
				},
				Endpoints: []EndpointConfig{
					{Name: "test", Target: "http://localhost:8000"},
				},
				Storage: StorageConfig{
					Path: "/tmp/test.jsonl",
				},
				Streaming: StreamingConfig{
					MaxAuditBodySize:       tt.maxBodySize,
					StreamTimeout:          tt.streamTimeout,
					EnableSequenceTracking: true,
				},
			}

			err := cfg.Validate()
			if tt.expectError {
				if err == nil {
					t.Errorf("Expected validation error but got none")
				} else if tt.errorContains != "" && !contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
