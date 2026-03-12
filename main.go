package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/redis/go-redis/v9"
	"github.com/twilio/twilio-go"
	openapi "github.com/twilio/twilio-go/rest/api/v2010"
)

var (
	redisClient *redis.Client
	ctx         = context.Background()
)

func main() {
	fmt.Println("Initializing Standalone Telephony/STT Gateway...")

	// Attempt to load .env from parent and current dir
	_ = godotenv.Load("../.env")
	_ = godotenv.Load(".env")

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	// Set up Redis
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("Invalid Redis URL: %v", err)
	}
	redisClient = redis.NewClient(opts)
	
	if err := redisClient.Ping(ctx).Err(); err != nil {
		log.Printf("Redis connection error: %v", err)
	} else {
		fmt.Println("Connected to Redis successfully.")
	}

	// Setup HTTP server
	http.HandleFunc("/", handleConnection)
	
	// Setup UI endpoints
	http.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "public/index.html")
	})
	
	http.HandleFunc("/ui-ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("UI Upgrade error:", err)
			return
		}
		stats.AddUIConn(conn)
	})

	// Setup Twilio Call API endpoints
	http.HandleFunc("/api/call", handleApiCall)
	http.HandleFunc("/twiml", handleTwiml)

	// Start Background state updater for the dashboard
	StartStateBroadcaster()

	fmt.Printf("\nTelephony Gateway listening on ws://localhost:%s\n", port)
	fmt.Printf("Live Analytics Dashboard available at: http://localhost:%s/ui\n", port)
	fmt.Println(`Point your Twilio Application's "TwiML Media Stream (wss://)" URL here!`)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Fatal error starting Telephony Gateway: %v", err)
	}
}

func handleApiCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	var req struct {
		Number1 string `json:"number1"`
		Number2 string `json:"number2"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	client := twilio.NewRestClient()

	baseURL := os.Getenv("TWILIO_WEBHOOK_BASE_URL")
	if baseURL == "" {
		streamURL := os.Getenv("TWILIO_MEDIA_STREAM_URL")
		if streamURL != "" {
			baseURL = strings.Replace(streamURL, "wss://", "https://", 1)
			baseURL = strings.Replace(baseURL, "ws://", "http://", 1)
		} else {
			baseURL = "https://" + r.Host
		}
	}

	callbackURL := fmt.Sprintf("%s/twiml?number2=%s", baseURL, url.QueryEscape(req.Number2))

	params := &openapi.CreateCallParams{}
	params.SetTo(req.Number1)
	params.SetFrom(os.Getenv("TWILIO_DEMO_FROM_NUMBER"))
	params.SetUrl(callbackURL)

	log.Printf("Initiating call to %s with callback %s", req.Number1, callbackURL)
	resp, err := client.Api.CreateCall(params)
	if err != nil {
		log.Printf("Twilio API Error: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"sid":     *resp.Sid,
	})
}

func handleTwiml(w http.ResponseWriter, r *http.Request) {
	number2 := r.URL.Query().Get("number2")

	streamURL := os.Getenv("TWILIO_MEDIA_STREAM_URL")
	if streamURL == "" {
		streamURL = "wss://" + r.Host + "/"
	}

	twiml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
    <Start>
        <Stream url="%s">
            <Parameter name="leg" value="caller" />
        </Stream>
    </Start>
    <Dial>%s</Dial>
</Response>`, streamURL, number2)

	log.Printf("Rendered TwiML connecting to %s via %s", streamURL, number2)

	w.Header().Set("Content-Type", "text/xml")
	w.Write([]byte(twiml))
}
