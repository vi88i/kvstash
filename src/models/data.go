// Package models defines data structures for KVStash API requests, responses, and internal storage
package models

// KVStashRequest represents a key-value pair in API requests
type KVStashRequest struct {
	// Key is the unique identifier for the value
	Key string `json:"key"`

	// Value is the data associated with the key
	Value string `json:"value"`
}

// KVStashResponse represents the API response structure
type KVStashResponse struct {
	// Success indicates whether the operation completed successfully
	Success bool `json:"success"`

	// Message provides additional information about the operation result
	Message string `json:"message"`

	// Data contains the retrieved key-value pair for successful GET requests
	Data *KVStashRequest `json:"data"`
}
