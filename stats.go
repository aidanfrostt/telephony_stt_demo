package main

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// CallState holds metrics for a single active call
type CallState struct {
	CallID    string    `json:"callId"`
	StartTime time.Time `json:"startTime"`
	BytesRead int64     `json:"bytesRead"`
	Status    string    `json:"status"`
}

// GlobalStats tracks the entire application's performance and active sessions
type GlobalStats struct {
	mu sync.RWMutex

	ActiveCalls   map[string]*CallState `json:"activeCalls"`
	TotalCalls    int64                 `json:"totalCalls"`
	BytesTotal    int64                 `json:"bytesTotal"`
	
	UIConnections map[*websocket.Conn]bool `json:"-"`
}

var stats = &GlobalStats{
	ActiveCalls:   make(map[string]*CallState),
	TotalCalls:    0,
	BytesTotal:    0,
	UIConnections: make(map[*websocket.Conn]bool),
}

// --- Event Functions ---
func (s *GlobalStats) AddCall(callID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ActiveCalls[callID] = &CallState{
		CallID:    callID,
		StartTime: time.Now(),
		BytesRead: 0,
		Status:    "Active",
	}
	s.TotalCalls++
	
	s.broadcastState()
}

func (s *GlobalStats) RemoveCall(callID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if call, exists := s.ActiveCalls[callID]; exists {
		s.BytesTotal += call.BytesRead
		delete(s.ActiveCalls, callID)
	}
	
	s.broadcastState()
}

func (s *GlobalStats) AddBytesRead(callID string, bytes int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if call, exists := s.ActiveCalls[callID]; exists {
		call.BytesRead += bytes
	}
	// We don't broadcast on every byte slice to avoid flooding UI websockets; 
	// instead we rely on a periodic flush or just final count.
}

func (s *GlobalStats) BroadcastTranscript(callID, text string, isFinal bool) {
	// Directly send transcript packets to the UI, bypassing full state serialization
	msg := map[string]interface{}{
		"type":    "transcript",
		"callId":  callID,
		"text":    text,
		"isFinal": isFinal,
	}
	s.broadcastRaw(msg)
}

// --- UI Broadcasting ---

func (s *GlobalStats) AddUIConn(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.UIConnections[conn] = true
	// Send initial state immediately
	s.sendStateTo(conn)
}

func (s *GlobalStats) RemoveUIConn(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.UIConnections, conn)
	conn.Close()
}

// broadcastState sends the complete snapshot of active calls and totals
// It should only be called from inside a locked mutex to prevent deadlocks,
// but actually json.Marshal might be slow, so we copy state first.
func (s *GlobalStats) broadcastState() {
	// Build snapshot
	snapshot := map[string]interface{}{
		"type":        "state_update",
		"activeCalls": s.ActiveCalls,
		"totalCalls":  s.TotalCalls,
		"bytesTotal":  s.BytesTotal,
	}
	
	for conn := range s.UIConnections {
		err := conn.WriteJSON(snapshot)
		if err != nil {
			log.Printf("Error broadcasting to UI: %v", err)
			conn.Close()
			delete(s.UIConnections, conn)
		}
	}
}

// broadcastRaw sends arbitrary JSON. Grabs read lock.
func (s *GlobalStats) broadcastRaw(msg interface{}) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for conn := range s.UIConnections {
		err := conn.WriteJSON(msg)
		if err != nil {
			log.Printf("Error broadcasting to UI: %v", err)
			conn.Close()
			// Cannot delete from map while ranging with read lock.
			// It will be cleaned up on the next broadcastState.
		}
	}
}

// sendStateTo sends the current state snapshot to a SINGLE connection.
// Requires caller to hold mutex (usually called inside AddUIConn).
func (s *GlobalStats) sendStateTo(conn *websocket.Conn) {
	snapshot := map[string]interface{}{
		"type":        "state_update",
		"activeCalls": s.ActiveCalls,
		"totalCalls":  s.TotalCalls,
		"bytesTotal":  s.BytesTotal,
	}
	conn.WriteJSON(snapshot)
}

// StartStateBroadcaster sits in a background loop and pushes the byte counts 
// down to the UI every 2 seconds when there are active calls.
func StartStateBroadcaster() {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		for range ticker.C {
			stats.mu.RLock()
			count := len(stats.ActiveCalls)
			uicount := len(stats.UIConnections)
			stats.mu.RUnlock()

			if count > 0 && uicount > 0 {
				// Lock again just to grab snapshot & broadcast
				stats.mu.Lock()
				stats.broadcastState()
				stats.mu.Unlock()
			}
		}
	}()
}
