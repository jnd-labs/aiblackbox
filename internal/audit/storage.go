package audit

import (
	"github.com/aiblackbox/proxy/internal/models"
)

// Storage defines the interface for persisting audit entries
// Implementations must be thread-safe
type Storage interface {
	// Write persists a single audit entry
	// Returns an error if the write operation fails
	Write(entry *models.AuditEntry) error

	// Close cleanly shuts down the storage
	// Must be called before application termination
	Close() error
}
