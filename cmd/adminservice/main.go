package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {

	log.Println("Admin service running on PORT :9003")

	http.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Welcome Admin!")
	})

	http.ListenAndServe(":9003", nil)
}
