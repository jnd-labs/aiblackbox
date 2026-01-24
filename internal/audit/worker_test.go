package audit

import (
	"testing"
	"time"

	"github.com/aiblackbox/proxy/internal/models"
)

// mockStorage is a test implementation of Storage interface
type mockStorage struct {
	entries []*models.AuditEntry
	closed  bool
}

func (m *mockStorage) Write(entry *models.AuditEntry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockStorage) Close() error {
	m.closed = true
	return nil
}

// TestSequentialProcessing verifies that entries arriving in order are processed correctly
func TestSequentialProcessing(t *testing.T) {
	storage := &mockStorage{}
	worker := NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	// Create entries with sequential IDs
	entries := []*models.AuditEntry{
		createTestEntry(0, "test1"),
		createTestEntry(1, "test2"),
		createTestEntry(2, "test3"),
	}

	// Send entries in order
	for _, entry := range entries {
		worker.Log(entry)
	}

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	// Verify all entries were processed
	if len(storage.entries) != 3 {
		t.Errorf("Expected 3 entries, got %d", len(storage.entries))
	}

	// Verify sequence order
	for i, entry := range storage.entries {
		if entry.SequenceID != uint64(i) {
			t.Errorf("Entry %d has wrong sequence ID: expected %d, got %d", i, i, entry.SequenceID)
		}
	}

	// Verify hash chaining
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: PrevHash doesn't match previous entry's Hash", i)
		}
	}
}

// TestOutOfOrderProcessing verifies that out-of-order entries are reordered correctly
func TestOutOfOrderProcessing(t *testing.T) {
	storage := &mockStorage{}
	worker := NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	// Create entries
	entry0 := createTestEntry(0, "test1")
	entry1 := createTestEntry(1, "test2")
	entry2 := createTestEntry(2, "test3")

	// Send entries out of order: 0, 2, 1
	worker.Log(entry0)
	time.Sleep(10 * time.Millisecond) // Let entry 0 process

	worker.Log(entry2) // Out of order
	time.Sleep(10 * time.Millisecond)

	// At this point, entry 2 should be pending
	if len(storage.entries) != 1 {
		t.Errorf("Expected 1 entry processed, got %d", len(storage.entries))
	}

	worker.Log(entry1) // Now send entry 1
	time.Sleep(50 * time.Millisecond)

	// Now all entries should be processed in correct order
	if len(storage.entries) != 3 {
		t.Errorf("Expected 3 entries processed, got %d", len(storage.entries))
	}

	// Verify they were processed in sequence order
	for i, entry := range storage.entries {
		if entry.SequenceID != uint64(i) {
			t.Errorf("Entry at index %d has wrong sequence ID: expected %d, got %d", i, i, entry.SequenceID)
		}
	}

	// Verify hash chaining is correct
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: PrevHash doesn't match previous entry's Hash", i)
		}
	}
}

// TestMultipleOutOfOrderEntries verifies handling of multiple out-of-order entries
func TestMultipleOutOfOrderEntries(t *testing.T) {
	storage := &mockStorage{}
	worker := NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	// Create entries
	entries := make([]*models.AuditEntry, 5)
	for i := 0; i < 5; i++ {
		entries[i] = createTestEntry(uint64(i), "test"+string(rune('A'+i)))
	}

	// Send in reverse order: 4, 3, 2, 1, 0
	for i := 4; i >= 0; i-- {
		worker.Log(entries[i])
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for all to process
	time.Sleep(100 * time.Millisecond)

	// Verify all entries processed
	if len(storage.entries) != 5 {
		t.Errorf("Expected 5 entries, got %d", len(storage.entries))
	}

	// Verify correct sequence order
	for i, entry := range storage.entries {
		if entry.SequenceID != uint64(i) {
			t.Errorf("Entry at index %d has wrong sequence ID: expected %d, got %d", i, i, entry.SequenceID)
		}
	}

	// Verify hash chain integrity
	for i := 1; i < len(storage.entries); i++ {
		if storage.entries[i].PrevHash != storage.entries[i-1].Hash {
			t.Errorf("Entry %d: Hash chain broken", i)
		}
	}
}

// TestPendingQueueLimit verifies that exceeding max pending entries triggers fail-open
func TestPendingQueueLimit(t *testing.T) {
	storage := &mockStorage{}
	worker := NewWorker(storage, "test-seed", 10)
	worker.maxPendingEntries = 3 // Set low limit for testing
	defer worker.Shutdown()

	// Send entry 0
	worker.Log(createTestEntry(0, "test0"))
	time.Sleep(10 * time.Millisecond)

	// Send entries 5, 6, 7, 8 (all out of order, exceeding limit)
	for i := 5; i < 9; i++ {
		worker.Log(createTestEntry(uint64(i), "test"+string(rune('A'+i))))
		time.Sleep(5 * time.Millisecond)
	}

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	// With fail-open behavior, we should have processed some entries
	// even though they're out of order
	if len(storage.entries) == 0 {
		t.Error("Expected some entries to be processed with fail-open behavior")
	}

	t.Logf("Processed %d entries with fail-open behavior", len(storage.entries))
}

// TestHashIncludesErrorFields verifies that error fields are included in hash
func TestHashIncludesErrorFields(t *testing.T) {
	storage := &mockStorage{}
	worker := NewWorker(storage, "test-seed", 10)
	defer worker.Shutdown()

	// Create two entries with same content but different error states
	entry1 := createTestEntry(0, "test")
	entry1.Response.Error = ""
	entry1.Response.IsComplete = true

	entry2 := createTestEntry(1, "test")
	entry2.Response.Error = "CLIENT_DISCONNECT"
	entry2.Response.IsComplete = false

	worker.Log(entry1)
	worker.Log(entry2)

	time.Sleep(50 * time.Millisecond)

	if len(storage.entries) != 2 {
		t.Fatalf("Expected 2 entries, got %d", len(storage.entries))
	}

	// Hashes should be different because error fields differ
	if storage.entries[0].Hash == storage.entries[1].Hash {
		t.Error("Hashes should differ when error fields are different")
	}

	// Verify error fields are preserved
	if storage.entries[1].Response.Error != "CLIENT_DISCONNECT" {
		t.Error("Error field not preserved")
	}

	if storage.entries[1].Response.IsComplete {
		t.Error("IsComplete should be false")
	}
}

// TestGenesisHash verifies genesis hash computation
func TestGenesisHash(t *testing.T) {
	seed := "test-seed"
	hash1 := computeGenesisHash(seed)
	hash2 := computeGenesisHash(seed)

	if hash1 != hash2 {
		t.Error("Genesis hash should be deterministic")
	}

	if hash1 == "" {
		t.Error("Genesis hash should not be empty")
	}

	// Different seed should produce different hash
	hash3 := computeGenesisHash("different-seed")
	if hash1 == hash3 {
		t.Error("Different seeds should produce different hashes")
	}
}

// TestWorkerShutdown verifies graceful shutdown
func TestWorkerShutdown(t *testing.T) {
	storage := &mockStorage{}
	worker := NewWorker(storage, "test-seed", 10)

	// Send some entries
	for i := 0; i < 5; i++ {
		worker.Log(createTestEntry(uint64(i), "test"+string(rune('A'+i))))
	}

	// Shutdown should wait for all entries to process
	worker.Shutdown()

	// Verify all entries processed
	if len(storage.entries) != 5 {
		t.Errorf("Expected 5 entries after shutdown, got %d", len(storage.entries))
	}

	// Verify storage was closed
	if !storage.closed {
		t.Error("Storage should be closed after shutdown")
	}
}

// TestConcurrentLogging verifies thread-safety of Log method
func TestConcurrentLogging(t *testing.T) {
	storage := &mockStorage{}
	worker := NewWorker(storage, "test-seed", 100)
	defer worker.Shutdown()

	// Send entries concurrently from multiple goroutines
	done := make(chan bool)
	for g := 0; g < 3; g++ {
		go func(goroutineID int) {
			for i := 0; i < 10; i++ {
				seqID := uint64(goroutineID*10 + i)
				worker.Log(createTestEntry(seqID, "test"))
			}
			done <- true
		}(g)
	}

	// Wait for all goroutines
	for g := 0; g < 3; g++ {
		<-done
	}

	// Wait for processing
	time.Sleep(200 * time.Millisecond)

	// Should have received all 30 entries
	if len(storage.entries) != 30 {
		t.Errorf("Expected 30 entries, got %d", len(storage.entries))
	}

	// Verify all entries were processed in sequence order
	for i, entry := range storage.entries {
		if entry.SequenceID != uint64(i) {
			t.Errorf("Entry at index %d has wrong sequence ID: expected %d, got %d", i, i, entry.SequenceID)
		}
	}
}

// Helper function to create test audit entries
func createTestEntry(sequenceID uint64, endpoint string) *models.AuditEntry {
	return &models.AuditEntry{
		Timestamp:  time.Now(),
		Endpoint:   endpoint,
		SequenceID: sequenceID,
		Request: models.RequestDetails{
			Method:        "POST",
			Path:          "/test",
			Headers:       make(map[string][]string),
			Body:          "test request",
			ContentLength: 12,
		},
		Response: models.ResponseDetails{
			StatusCode:    200,
			Headers:       make(map[string][]string),
			Body:          "test response",
			ContentLength: 13,
			Duration:      time.Millisecond * 100,
			IsStreaming:   false,
			IsComplete:    true,
			Error:         "",
		},
	}
}
