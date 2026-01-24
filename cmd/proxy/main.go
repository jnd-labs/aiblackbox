package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jnd-labs/aiblackbox/internal/audit"
	"github.com/jnd-labs/aiblackbox/internal/config"
	"github.com/jnd-labs/aiblackbox/internal/proxy"
)

const (
	// Buffer size for the audit channel
	// Allows up to 1000 requests to be queued before blocking
	auditBufferSize = 1000

	// Graceful shutdown timeout
	shutdownTimeout = 30 * time.Second
)

func main() {
	log.Println("Starting AIBlackBox Proxy...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Configuration loaded: %d endpoints defined", len(cfg.Endpoints))
	for _, ep := range cfg.Endpoints {
		log.Printf("  - %s -> %s", ep.Name, ep.Target)
	}

	// Initialize storage
	storage, err := audit.NewFileStorage(cfg.Storage.Path)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}
	log.Printf("Storage initialized: %s", cfg.Storage.Path)

	// Initialize audit worker
	auditWorker := audit.NewWorker(storage, cfg.Server.GenesisSeed, auditBufferSize)
	log.Println("Audit worker started")

	// Create prox handler
	handler := proxy.NewHandler(cfg, auditWorker)

	// Create HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Server listening on port %d", cfg.Server.Port)
		log.Println("Ready to proxy requests")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutdown signal received, gracefully shutting down...")

	// Create shutdown context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// Shutdown HTTP server
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Error during server shutdown: %v", err)
	}

	// Shutdown audit worker (processes remaining entries)
	log.Println("Flushing remaining audit entries...")
	auditWorker.Shutdown()

	log.Println("Shutdown complete")
}
