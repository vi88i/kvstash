// Package svc implements the HTTP server and API handlers for KVStash
package svc

import (
	"encoding/json"
	"errors"
	"kvstash/models"
	"kvstash/store"
	"log"
	"net/http"
	"slices"
)

// kvStore is the global store instance used by the HTTP handlers
var kvStore *store.Store

// apiHandler processes HTTP requests for key-value operations
// Supports POST for setting values and GET for retrieving values
// Returns JSON responses with success status and data
func apiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Helper function to send JSON response
	sendResponse := func(statusCode int, success bool, message string, data *models.KVStashRequest) {
		w.WriteHeader(statusCode)
		respData := models.KVStashResponse{
			Success: success,
			Message: message,
			Data:    data,
		}
		if err := json.NewEncoder(w).Encode(respData); err != nil {
			log.Printf("apiHandler: failed to encode response: %v", err)
		}
	}

	// Validate HTTP method
	if !slices.Contains([]string{http.MethodPost, http.MethodGet}, r.Method) {
		sendResponse(http.StatusMethodNotAllowed, false, "", nil)
		return
	}

	// Decode request body
	var reqData models.KVStashRequest
	if err := json.NewDecoder(r.Body).Decode(&reqData); err != nil {
		log.Printf("apiHandler: failed to decode request body: %v", err)
		sendResponse(http.StatusBadRequest, false, "invalid json body", nil)
		return
	}

	switch r.Method {
	case http.MethodPost:
		// Validate value is non-empty
		if len(reqData.Value) == 0 {
			sendResponse(http.StatusBadRequest, false, "value should be non-empty", nil)
			return
		}

		// Attempt to set key-value pair
		if err := kvStore.Set(&reqData); err != nil {
			log.Printf("apiHandler: failed to set key: %v", err)
			// Check if this is a validation error (400) or server error (500)
			if errors.Is(err, store.ErrEmptyKey) ||
				errors.Is(err, store.ErrKeyTooLarge) ||
				errors.Is(err, store.ErrValueTooLarge) {
				sendResponse(http.StatusBadRequest, false, err.Error(), nil)
			} else {
				sendResponse(http.StatusInternalServerError, false, "write failed", nil)
			}
			return
		}

		sendResponse(http.StatusCreated, true, "", nil)

	case http.MethodGet:
		// Attempt to get value
		value, err := kvStore.Get(&reqData)
		if err != nil {
			log.Printf("apiHandler: failed to get key: %v", err)
			// Check if key not found (404) or server error (500)
			if errors.Is(err, store.ErrKeyNotFound) {
				sendResponse(http.StatusNotFound, false, "key not found", nil)
			} else {
				sendResponse(http.StatusInternalServerError, false, "read failed", nil)
			}
			return
		}

		sendResponse(http.StatusOK, true, "", &models.KVStashRequest{
			Key:   reqData.Key,
			Value: value,
		})

	default:
		sendResponse(http.StatusInternalServerError, false, "unable to process request", nil)
	}
}

// StartHTTPServer initializes and starts the HTTP server on port 8080
// It registers the API handler and blocks until the server terminates
// Accepts a Store instance for handling key-value operations
func StartHTTPServer(s *store.Store) {
	kvStore = s
	http.HandleFunc("/kvstash", apiHandler)

	port := ":8080"
	log.Printf("StartHTTPServer: listening on http://localhost%v", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
