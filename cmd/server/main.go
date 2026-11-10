package main

import (
	"log"
	"net/http"
	"os"

	"github.com/edoricardo/tcg-party-locator/internal/app"
)

func main() {
	store := app.NewEventStore()
	handler := app.NewHandler(store)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	addr := ":" + port
	log.Printf("Starting TCG Party Locator on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
