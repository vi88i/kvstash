// Package main is the entry point for the KVStash server application
package main

import (
	"kvstash/src/constants"
	"kvstash/src/store"
	"kvstash/src/svc"
	"log"
)

// main initializes the store and starts the HTTP server
func main() {
	// Initialize the store
	kvStore, err := store.NewStore(constants.DBPath)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}
	defer kvStore.Close()

	// Start the HTTP server
	svc.StartHTTPServer(kvStore)
}
