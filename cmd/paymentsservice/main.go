package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	log.Println("Payments service running on PORT :9002")

	mux := http.NewServeMux()

	mux.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "checkout initiated"})
	})

	mux.HandleFunc("/history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]string{
			{"id": "txn_001", "amount": "499", "status": "success"},
			{"id": "txn_002", "amount": "199", "status": "success"},
		})
	})

	http.ListenAndServe(":9002", mux)
}
