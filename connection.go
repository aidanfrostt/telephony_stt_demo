package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"os"

	"github.com/gorilla/websocket"
)

var audioBufferPool = sync.Pool{
	New: func() interface{} {
		// Allocate 512 bytes for typical Twilio 160-byte payload + base64 overhead, reusable array
		b := make([]byte, 0, 512)
		return &b
	},
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type TwilioEvent struct {
	Event            string           `json:"event"`
	SequenceNumber   string           `json:"sequenceNumber,omitempty"`
	StreamSid        string           `json:"streamSid,omitempty"`
	Start            *TwilioStart     `json:"start,omitempty"`
	Media            *TwilioMedia     `json:"media,omitempty"`
	Stop             *TwilioStop      `json:"stop,omitempty"`
	Mark             *TwilioMark      `json:"mark,omitempty"`
}

type TwilioStart struct {
	AccountSid       string                 `json:"accountSid"`
	StreamSid        string                 `json:"streamSid"`
	CallSid          string                 `json:"callSid"`
	Tracks           []string               `json:"tracks"`
	CustomParameters map[string]string      `json:"customParameters"`
	MediaFormat      map[string]interface{} `json:"mediaFormat"`
}

type TwilioMedia struct {
	Track   string `json:"track"`
	Chunk   string `json:"chunk"`
	Timestamp string `json:"timestamp"`
	Payload string `json:"payload"`
}

type TwilioStop struct {
	AccountSid string `json:"accountSid"`
	CallSid    string `json:"callSid"`
}

type TwilioMark struct {
	Name string `json:"name"`
}

func handleConnection(w http.ResponseWriter, r *http.Request) {
	if !websocket.IsWebSocketUpgrade(r) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("W-L-Calls Standalone Telephony STT Gateway - WebSocket server active"))
		return
	}

	// Upgrade original request to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Upgrade error: %v", err)
		return
	}
	defer conn.Close()

	log.Println("=> New WebSocket connection received.")

	var sttConn *STTConnection
	var callID string

	// Setup callback to send transcription events back to Twilio stream (or console, or via Redis)
	// Right now the Node logic was just sending it back to the websocket
	onResult := func(result STTResult) {
		log.Printf("[Transcript %v]: %s\n", result.IsFinal, result.Text)
		
		// Broadcast to Global State / UI Dashboard
		if callID != "" {
			stats.BroadcastTranscript(callID, result.Text, result.IsFinal)
		}

		msg := map[string]interface{}{
			"event":   "transcript",
			"text":    result.Text,
			"isFinal": result.IsFinal,
		}
		
		msgBytes, _ := json.Marshal(msg)
		conn.WriteMessage(websocket.TextMessage, msgBytes)
	}

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Twilio WebSocket read error: %v", err)
			break
		}

		var event TwilioEvent
		if err := json.Unmarshal(message, &event); err != nil {
			log.Printf("Failed to unmarshal Twilio event: %v", err)
			continue
		}

		switch event.Event {
		case "start":
			callID := event.Start.CallSid
			leg := "caller"
			speaker := "customer"

			if event.Start.CustomParameters != nil {
				if cID, ok := event.Start.CustomParameters["callId"]; ok {
					callID = cID
				}
				if paramLeg, ok := event.Start.CustomParameters["leg"]; ok {
					if paramLeg == "target" {
						leg = "target"
						speaker = "agent"
					}
				}
			}
			
			log.Printf("Twilio stream started. CallID: %s, Leg: %s", callID, leg)

			deepgramKey := os.Getenv("DEEPGRAM_API_KEY")
			opts := DeepgramOptions{
				Encoding:   "mulaw",
				SampleRate: 8000,
				Speaker:    speaker,
			}

			if deepgramKey != "" {
				sttConn, err = CreateDeepgramConnection(callID, "standalone-demo", redisClient, onResult, opts)
				if err != nil {
					log.Printf("Failed to create Deepgram connection: %v", err)
				}
			} else {
				log.Println("No DEEPGRAM_API_KEY set. Falling back to Mock STT connection.")
				sttConn = CreateMockConnection(callID, "standalone-demo", redisClient, onResult, speaker)
			}
			
			// Tell Global State manager we started a call
			stats.AddCall(callID)

		case "media":
			if sttConn != nil && event.Media != nil && event.Media.Payload != "" {
				// Base64 decode audio payload
				audioDataPtr := audioBufferPool.Get().(*[]byte)
				audioData := *audioDataPtr
				
				// Decode directly into the pooled buffer
				decodedLen, err := base64.StdEncoding.Decode(audioData[:cap(audioData)], []byte(event.Media.Payload))
				if err != nil {
					log.Printf("Base64 decode error: %v", err)
					audioBufferPool.Put(audioDataPtr)
					continue
				}
				
				// Keep track of bytes processed for dashboard
				stats.AddBytesRead(callID, int64(decodedLen))

				// Send non-blocking or blocking to the audio channel
				// The receiving side (stt.go) will need to copy or process and then we'd technically put it back,
				// but because stt.go pushes to a channel and processes async, it's safer to just allocate strings or let stt.go copy.
				// Wait, if we pass the slice to deeply async channels, we can't safely Pool.Put here.
				// For simplicity of this optimization: We allocate a fresh slice bounded by the decoded length,
				// or we let stt.go put it back (requires changing stt.go).
				// We'll stick to simple slice but track bytes. Let's rewrite this part correctly.
				
				finalAudio := make([]byte, decodedLen)
				copy(finalAudio, audioData[:decodedLen])
				audioBufferPool.Put(audioDataPtr)

				select {
				case sttConn.AudioChan <- finalAudio:
				default:
					log.Println("Warning: Audio channel full, dropping frame")
				}
			}

		case "stop":
			log.Println("Call Stream Stopped.")
			if callID != "" {
				stats.RemoveCall(callID)
			}
			if sttConn != nil {
				sttConn.Close()
				sttConn = nil
			}

		case "mark":
			// Twilio marks
		}
	}

	// Clean up if the websocket unceremoniously closes
	if callID != "" {
		stats.RemoveCall(callID)
	}
	if sttConn != nil {
		sttConn.Close()
		sttConn = nil
	}
	log.Println("WebSocket Disconnected.")
}
