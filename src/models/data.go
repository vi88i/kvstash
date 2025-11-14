package models

type KVStashRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type KVStashResponse struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    *KVStashRequest `json:"data"`
}
