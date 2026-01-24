package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// Config represents the entire application configuration
type Config struct {
	Server    ServerConfig     `mapstructure:"server"`
	Endpoints []EndpointConfig `mapstructure:"endpoints"`
	Storage   StorageConfig    `mapstructure:"storage"`
	Streaming StreamingConfig  `mapstructure:"streaming"`
}

// ServerConfig contains server-level settings
type ServerConfig struct {
	Port        int    `mapstructure:"port"`
	GenesisSeed string `mapstructure:"genesis_seed"`
}

// EndpointConfig defines a single named endpoint for proxying
type EndpointConfig struct {
	Name   string `mapstructure:"name"`
	Target string `mapstructure:"target"`
}

// StorageConfig defines where and how audit logs are stored
type StorageConfig struct {
	Path string `mapstructure:"path"`
}

// StreamingConfig defines settings for handling streaming (SSE) responses
type StreamingConfig struct {
	// MaxAuditBodySize is the maximum response body size to capture for audit logs (in bytes)
	// Responses exceeding this limit will be truncated in the audit log but forwarded completely to the client
	// Default: 10485760 (10 MB)
	MaxAuditBodySize int64 `mapstructure:"max_audit_body_size"`

	// StreamTimeout is the maximum duration (in seconds) to wait for a stream to complete
	// If a stream exceeds this timeout, it will be force-finalized with a timeout marker
	// Default: 300 (5 minutes)
	StreamTimeout int `mapstructure:"stream_timeout"`

	// EnableSequenceTracking enables sequence-based ordering for out-of-order stream completions
	// When true, maintains hash chain integrity even when concurrent streams complete out of order
	// Default: true
	EnableSequenceTracking bool `mapstructure:"enable_sequence_tracking"`
}

// MediaConfig defines settings for handling large media content (images, etc.)
type MediaConfig struct {
	// EnableExtraction enables extraction of large Base64-encoded media to separate files
	// When false, all content is stored inline in the audit log
	// Default: true
	EnableExtraction bool `mapstructure:"enable_extraction"`

	// MinSizeKB is the minimum size (in KB) for media to be extracted
	// Base64 images smaller than this will remain inline in the audit log
	// Default: 100 (100 KB)
	MinSizeKB int64 `mapstructure:"min_size_kb"`

	// StoragePath is the directory where extracted media files will be stored
	// Files are organized by date: {storage_path}/{YYYY-MM-DD}/seq_{N}_{type}_{index}.{ext}
	// Default: "./logs/media"
	StoragePath string `mapstructure:"storage_path"`
}

// Load reads configuration from config.yaml and environment variables
// Environment variables take precedence and must be prefixed with ABB_
// Example: ABB_SERVER_PORT=9000
func Load() (*Config, error) {
	v := viper.New()

	// Set config file settings
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("/app")

	// Enable environment variable override with ABB_ prefix
	v.SetEnvPrefix("ABB")
	v.AutomaticEnv()

	// Set defaults
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.genesis_seed", "aiblackbox-default-seed")
	v.SetDefault("storage.path", "./logs/audit.jsonl")
	v.SetDefault("streaming.max_audit_body_size", 10485760) // 10 MB
	v.SetDefault("streaming.stream_timeout", 300)           // 5 minutes
	v.SetDefault("streaming.enable_sequence_tracking", true)

	// Read config file
	if err := v.ReadInConfig(); err != nil {
		// Config file is optional if all values are provided via env vars
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
	}

	// Unmarshal into struct
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.Server.GenesisSeed == "" {
		return fmt.Errorf("genesis_seed cannot be empty")
	}

	if len(c.Endpoints) == 0 {
		return fmt.Errorf("at least one endpoint must be defined")
	}

	// Validate each endpoint
	endpointNames := make(map[string]bool)
	for _, ep := range c.Endpoints {
		if ep.Name == "" {
			return fmt.Errorf("endpoint name cannot be empty")
		}
		if ep.Target == "" {
			return fmt.Errorf("endpoint target cannot be empty for: %s", ep.Name)
		}
		// Check for duplicate names
		if endpointNames[ep.Name] {
			return fmt.Errorf("duplicate endpoint name: %s", ep.Name)
		}
		endpointNames[ep.Name] = true
	}

	if c.Storage.Path == "" {
		return fmt.Errorf("storage path cannot be empty")
	}

	// Validate streaming configuration
	if c.Streaming.MaxAuditBodySize <= 0 {
		return fmt.Errorf("streaming.max_audit_body_size must be positive")
	}

	if c.Streaming.StreamTimeout <= 0 {
		return fmt.Errorf("streaming.stream_timeout must be positive")
	}

	return nil
}

// GetEndpoint retrieves an endpoint configuration by name
func (c *Config) GetEndpoint(name string) (EndpointConfig, bool) {
	for _, ep := range c.Endpoints {
		if ep.Name == name {
			return ep, true
		}
	}
	return EndpointConfig{}, false
}
