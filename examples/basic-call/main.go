package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/birddigital/signalwire-telephony/pkg/signalwire"
	"github.com/birddigital/signalwire-telephony/pkg/telephony"
)

func main() {
	// Initialize SignalWire client
	client := signalwire.NewClient(
		"your-project-id",
		"your-auth-token",
		"your-space.signalwire.com",
	)

	// Create audio bridge
	bridge := telephony.NewAudioStreamBridge()

	// Create WebSocket server
	audioServer := telephony.NewSignalWireAudioBridge(
		"your-project-id",
		"your-auth-token",
		"your-space.signalwire.com",
		bridge,
	)

	// Create HTTP handlers
	handlers := telephony.NewCallHandlers(audioServer, bridge)

	// Setup HTTP router
	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux)

	// Health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("OK"))
	})

	fmt.Println("Server starting on :8080...")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
