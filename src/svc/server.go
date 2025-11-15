// Package svc implements the HTTP server and API handlers for KVStash
package svc

import (
	"encoding/json"
	"kvstash/src/models"
	"kvstash/src/store"
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

	var err error
	var reqData models.KVStashRequest
	var respData = models.KVStashResponse{
		Success: false,
	}

	if !slices.Contains([]string{http.MethodPost, http.MethodGet}, r.Method) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		goto send
	}

	err = json.NewDecoder(r.Body).Decode(&reqData)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		respData.Message = "invalid json body"
		log.Printf("apiHandler: failed to decode request body: %v", err)
		goto send
	}

	switch r.Method {
	case http.MethodPost:
		if len(reqData.Value) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			respData.Message = "value should be non-empty"
			goto send
		}

		if err := kvStore.Set(&reqData); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			respData.Message = "write failed"
			log.Printf("apiHandler: failed to set key: %v", err)
			goto send
		}

		w.WriteHeader(http.StatusCreated)
		respData.Success = true
	case http.MethodGet:
		value, err := kvStore.Get(&reqData)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			respData.Message = "read failed"
			log.Printf("apiHandler: failed to get key: %v", err)
			goto send
		}

		w.WriteHeader(http.StatusOK)
		respData.Success = true
		respData.Data = &models.KVStashRequest{
			Key:   reqData.Key,
			Value: value,
		}
	default:
		w.WriteHeader(http.StatusInternalServerError)
		respData.Message = "unable to process request"
	}

send:
	json.
		NewEncoder(w).
		Encode(respData)
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
