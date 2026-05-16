package api

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Simple WebSocket implementation without external deps using HTTP chunked streaming (SSE)

type SSEClient struct {
	ch     chan []byte
	closed bool
	mu     sync.Mutex
}

func (c *SSEClient) Send(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	select {
	case c.ch <- data:
	default:
	}
}

func (c *SSEClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.ch)
	}
}

type Hub struct {
	mu      sync.RWMutex
	clients map[string][]*SSEClient // topic -> clients
}

var globalHub = &Hub{clients: make(map[string][]*SSEClient)}

func GetHub() *Hub { return globalHub }

func (h *Hub) Subscribe(topic string) *SSEClient {
	c := &SSEClient{ch: make(chan []byte, 512)}
	h.mu.Lock()
	h.clients[topic] = append(h.clients[topic], c)
	h.mu.Unlock()
	return c
}

func (h *Hub) Unsubscribe(topic string, c *SSEClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.clients[topic]
	nl := list[:0]
	for _, cl := range list {
		if cl != c {
			nl = append(nl, cl)
		}
	}
	h.clients[topic] = nl
	c.Close()
}

func (h *Hub) Publish(topic string, v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.RLock()
	clients := make([]*SSEClient, len(h.clients[topic]))
	copy(clients, h.clients[topic])
	h.mu.RUnlock()
	for _, c := range clients {
		c.Send(data)
	}
}

// SSEHandler handles a Server-Sent Events connection
func SSEHandler(topic string, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	client := globalHub.Subscribe(topic)
	defer globalHub.Unsubscribe(topic, client)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-client.ch:
			if !ok {
				return
			}
			_, _ = w.Write([]byte("data: "))
			_, _ = w.Write(data)
			_, _ = w.Write([]byte("\n\n"))
			flusher.Flush()
		}
	}
}
