package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	log.Println("User service running on PORT :9001")

	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello for user service.")
	})

	http.ListenAndServe(":9001", nil)
}
