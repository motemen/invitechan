package main

import (
	"log"
	"net/http"
	"os"

	"github.com/motemen/invitechan"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Printf("Listening to :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, http.HandlerFunc(invitechan.Do)))
}
