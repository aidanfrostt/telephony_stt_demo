package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

type STTResult struct {
	Text    string `json:"text"`
	IsFinal bool   `json:"is_final"`
	Speaker string `json:"speaker,omitempty"`
}

type STTConnection struct {
	AudioChan chan []byte
	Close     func()
}

type DeepgramOptions struct {
	Encoding   string
	SampleRate int
	Speaker    string
}

// Deepgram Live Transcription Events response format
type DGTranscriptResponse struct {
	Type    string `json:"type"`
	IsFinal bool   `json:"is_final"`
	Channel struct {
		Alternatives []struct {
			Transcript string `json:"transcript"`
		} `json:"alternatives"`
	} `json:"channel"`
}

func CreateDeepgramConnection(callID, agentID string, rdb *redis.Client, onResult func(STTResult), opts DeepgramOptions) (*STTConnection, error) {
	apiKey := os.Getenv("DEEPGRAM_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("DEEPGRAM_API_KEY is not set")
	}

	model := os.Getenv("DEEPGRAM_MODEL")
	if model == "" {
		model = "nova-2"
	}

	encoding := opts.Encoding
	if encoding == "" {
		encoding = "linear16" // defaults
	}

	sampleRate := opts.SampleRate
	if sampleRate == 0 {
		sampleRate = 16000
	}

	speaker := opts.Speaker
	if speaker == "" {
		speaker = "customer"
	}

	// Build connection URL
	u, err := url.Parse("wss://api.deepgram.com/v1/listen")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("model", model)
	q.Set("language", "multi")
	q.Set("smart_format", "true")
	q.Set("encoding", encoding)
	q.Set("sample_rate", fmt.Sprintf("%d", sampleRate))
	q.Set("channels", "1")
	q.Set("interim_results", "true")
	q.Set("endpointing", "200")
	q.Set("utterance_end_ms", "1000")
	q.Set("vad_events", "true")
	q.Set("filler_words", "true")
	q.Set("no_delay", "true")
	u.RawQuery = q.Encode()

	headers := http.Header{}
	headers.Add("Authorization", "Token "+apiKey)

	log.Printf("Deepgram: opening for call %s (model=%s, encoding=%s, rate=%d)\n", callID, model, encoding, sampleRate)
	
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.Dial(u.String(), headers)
	if err != nil {
		return nil, fmt.Errorf("deepgram dial error: %v", err)
	}

	log.Printf("Deepgram connected for call %s\n", callID)

	audioChan := make(chan []byte, 500) // Buffer for incoming audio
	done := make(chan struct{})

	// Goroutine to read from Deepgram
	go func() {
		defer func() {
			close(done)
			conn.Close()
		}()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("Deepgram read error for call %s: %v\n", callID, err)
				return
			}

			var resp DGTranscriptResponse
			if err := json.Unmarshal(message, &resp); err != nil {
				continue
			}

			if resp.Type == "Results" && len(resp.Channel.Alternatives) > 0 {
				transcript := resp.Channel.Alternatives[0].Transcript
				if transcript != "" {
					result := STTResult{
						Text:    transcript,
						IsFinal: resp.IsFinal,
						Speaker: speaker,
					}
					onResult(result)
				}
			}
		}
	}()

	// Goroutine to write audio to Deepgram
	go func() {
		defer conn.Close()
		for {
			select {
			case audio, ok := <-audioChan:
				if !ok {
					// Channel closed, graceful disconnect
					conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					return
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, audio); err != nil {
					log.Printf("Deepgram write error for call %s: %v\n", callID, err)
					return
				}
			case <-done:
				// Read loop exited
				return
			}
		}
	}()

	return &STTConnection{
		AudioChan: audioChan,
		Close: func() {
			close(audioChan)
		},
	}, nil
}

func CreateMockConnection(callID, agentID string, rdb *redis.Client, onResult func(STTResult), speaker string) *STTConnection {
	if speaker == "" {
		speaker = "customer"
	}

	audioChan := make(chan []byte, 500)
	done := make(chan struct{})

	phrases := []string{
		"Hello, how are you doing today?",
		"I wanted to follow up on our last conversation.",
		"That sounds great, tell me more about that.",
		"I completely understand your concern.",
		"Let me see what I can do for you.",
		"We have a few options available.",
		"Would that work for you?",
		"Thanks for your time today.",
	}

	go func() {
		ticker := time.NewTicker(2500 * time.Millisecond)
		defer ticker.Stop()
		
		phraseIdx := 0
		var chunkBytes int

		for {
			select {
			case audio, ok := <-audioChan:
				if !ok {
					return
				}
				chunkBytes += len(audio)
			case <-ticker.C:
				if chunkBytes > 0 {
					text := phrases[phraseIdx%len(phrases)]
					phraseIdx++
					chunkBytes = 0

					onResult(STTResult{
						Text:    text,
						IsFinal: true,
						Speaker: speaker,
					})
				}
			case <-done:
				return
			}
		}
	}()

	return &STTConnection{
		AudioChan: audioChan,
		Close: func() {
			close(done)
			close(audioChan)
		},
	}
}
