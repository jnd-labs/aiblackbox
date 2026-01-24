package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/jnd-labs/aiblackbox/internal/models"
)

// FileStorage implements Storage interface using JSON Lines format
// Each audit entry is written as a single line of JSON
type FileStorage struct {
	file *os.File
	mu   sync.Mutex
}

// NewFileStorage creates a new file-based storage
// Creates the directory if it doesn't exist
// Opens the file in append mode to preserve existing entries
func NewFileStorage(path string) (*FileStorage, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Open file in append mode (create if doesn't exist)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log file: %w", err)
	}

	return &FileStorage{
		file: file,
	}, nil
}

// Write appends a single audit entry to the log file
// Thread-safe: uses mutex to prevent concurrent writes
func (fs *FileStorage) Write(entry *models.AuditEntry) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	// Marshal to JSON
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal audit entry: %w", err)
	}

	// Append newline for JSON Lines format
	data = append(data, '\n')

	// Write to file
	if _, err := fs.file.Write(data); err != nil {
		return fmt.Errorf("failed to write to audit log: %w", err)
	}

	// Sync to disk for durability
	if err := fs.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync audit log: %w", err)
	}

	return nil
}

// Close flushes and closes the log file
func (fs *FileStorage) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if fs.file != nil {
		return fs.file.Close()
	}
	return nil
}
