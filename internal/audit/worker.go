package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"sync"

	"github.com/jnd-labs/aiblackbox/internal/models"
)

// Worker processes audit entries asynchronously with cryptographic hash chaining
// Uses a single goroutine to ensure sequential processing and deterministic hashing
// Supports out-of-order entry completion while maintaining hash chain integrity
type Worker struct {
	entries     chan *models.AuditEntry
	storage     Storage
	prevHash    string
	genesisSeed string
	done        chan struct{}

	// Sequence tracking for out-of-order handling
	expectedSeq    uint64
	pendingEntries map[uint64]*models.AuditEntry
	mu             sync.Mutex

	// Configuration
	maxPendingEntries int
}

// NewWorker creates and starts a new audit worker
// genesisSeed is used as the PrevHash for the first entry
// bufferSize determines how many entries can be queued before blocking
func NewWorker(storage Storage, genesisSeed string, bufferSize int) *Worker {
	w := &Worker{
		entries:           make(chan *models.AuditEntry, bufferSize),
		storage:           storage,
		prevHash:          computeGenesisHash(genesisSeed),
		genesisSeed:       genesisSeed,
		done:              make(chan struct{}),
		expectedSeq:       0,
		pendingEntries:    make(map[uint64]*models.AuditEntry),
		maxPendingEntries: 1000, // Prevent unbounded memory growth
	}

	// Start the worker goroutine
	go w.run()

	return w
}

// Log queues an audit entry for processing
// Non-blocking if buffer has space, blocks if buffer is full
func (w *Worker) Log(entry *models.AuditEntry) {
	w.entries <- entry
}

// Shutdown gracefully stops the worker
// Processes all remaining entries in the queue before closing
func (w *Worker) Shutdown() {
	close(w.entries)
	<-w.done
}

// run is the main worker loop that processes entries sequentially
// Handles out-of-order entries by maintaining a pending queue
func (w *Worker) run() {
	defer close(w.done)

	for entry := range w.entries {
		w.mu.Lock()

		// Check if this is the next expected sequence
		if entry.SequenceID == w.expectedSeq {
			// Process immediately
			w.processEntry(entry)
			w.expectedSeq++

			// Check for any pending entries that are now in sequence
			for {
				if nextEntry, exists := w.pendingEntries[w.expectedSeq]; exists {
					w.processEntry(nextEntry)
					delete(w.pendingEntries, w.expectedSeq)
					w.expectedSeq++
				} else {
					break
				}
			}

			// Log warning if pending queue is growing
			if len(w.pendingEntries) > 0 && len(w.pendingEntries)%100 == 0 {
				log.Printf("WARNING: Audit pending queue size: %d entries", len(w.pendingEntries))
			}
		} else {
			// Out of order - store for later processing
			if len(w.pendingEntries) >= w.maxPendingEntries {
				log.Printf("ERROR: Pending queue exceeded max size (%d), processing entry out of order: seq=%d, expected=%d",
					w.maxPendingEntries, entry.SequenceID, w.expectedSeq)
				// Process anyway to prevent blocking (fail-open behavior)
				w.processEntry(entry)
				w.expectedSeq = entry.SequenceID + 1
			} else {
				w.pendingEntries[entry.SequenceID] = entry
			}
		}

		w.mu.Unlock()
	}

	// Process any remaining pending entries on shutdown
	w.mu.Lock()
	if len(w.pendingEntries) > 0 {
		log.Printf("WARNING: Processing %d pending entries on shutdown (out of sequence order)", len(w.pendingEntries))
		// Process remaining entries (may be out of sequence due to missing entries)
		// This is fail-open behavior to ensure no data is lost
		for seq, entry := range w.pendingEntries {
			log.Printf("WARNING: Processing out-of-sequence entry: seq=%d, expected=%d", seq, w.expectedSeq)
			w.processEntry(entry)
		}
		// Clear the pending map
		w.pendingEntries = make(map[uint64]*models.AuditEntry)
	}
	w.mu.Unlock()

	// Close storage on shutdown
	if err := w.storage.Close(); err != nil {
		log.Printf("ERROR: Failed to close storage: %v", err)
	}
}

// processEntry handles the actual processing of a single audit entry
// Must be called with w.mu held
func (w *Worker) processEntry(entry *models.AuditEntry) {
	// Set the previous hash
	entry.PrevHash = w.prevHash

	// Compute the hash for this entry
	entry.Hash = w.computeHash(entry)

	// Write to storage
	if err := w.storage.Write(entry); err != nil {
		log.Printf("ERROR: Failed to write audit entry (seq=%d): %v", entry.SequenceID, err)
		// In production, this could trigger alerts
		// For MVP, we log and continue to maintain fail-open behavior
		return
	}

	// Update previous hash for next entry
	w.prevHash = entry.Hash
}

// computeHash generates the SHA-256 hash for an audit entry
// Hash = SHA256(Timestamp + Endpoint + RequestBody + ResponseBody + StatusCode + Error + IsComplete + PrevHash)
func (w *Worker) computeHash(entry *models.AuditEntry) string {
	h := sha256.New()

	// Write all components to the hash
	h.Write([]byte(entry.Timestamp.Format("2006-01-02T15:04:05.999999999Z07:00")))
	h.Write([]byte(entry.Endpoint))
	h.Write([]byte(entry.Request.Body))
	h.Write([]byte(entry.Response.Body))
	h.Write([]byte(strconv.Itoa(entry.Response.StatusCode)))
	h.Write([]byte(entry.Response.Error))
	h.Write([]byte(strconv.FormatBool(entry.Response.IsComplete)))
	h.Write([]byte(entry.PrevHash))

	return hex.EncodeToString(h.Sum(nil))
}

// computeGenesisHash creates the initial hash from the genesis seed
func computeGenesisHash(seed string) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("genesis:%s", seed)))
	return hex.EncodeToString(h.Sum(nil))
}
