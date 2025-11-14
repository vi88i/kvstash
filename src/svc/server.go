package svc

import (
	"encoding/json"
	"kvstash/src/models"
	"kvstash/src/store"
	"log"
	"net/http"
	"slices"
)

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
		log.Println(err)
		goto send
	}

	switch r.Method {
	case http.MethodPost:
		if len(reqData.Value) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			respData.Message = "value should be non-empty"
			goto send
		}

		if err := store.Set(&reqData); err != nil {
			w.WriteHeader(http.StatusInternalServerError)	
			respData.Message = "write failed"
			log.Println(err)
			goto send
		}

		w.WriteHeader(http.StatusCreated)
		respData.Success = true
	case http.MethodGet:
		value, err := store.Get(&reqData)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			respData.Message = "read failed"
			log.Println(err)
			goto send
		}

		w.WriteHeader(http.StatusFound)
		respData.Success = true
		respData.Data = &models.KVStashRequest{
			Key: reqData.Key,
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

func StartHTTPServer() {
	http.HandleFunc("/kvstash", apiHandler)

	port := ":8080"
	log.Printf("Listening on http://localhost%v", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
