package server

import (
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// wsClient represents a connected WebSocket client with its own write lock
type wsClient struct {
	conn         *websocket.Conn
	mu           sync.Mutex
	writeTimeout time.Duration
	lastActivity time.Time
	id           string
}

// wsClientPool manages connected WebSocket clients with per-connection locking
type wsClientPool struct {
	clients map[*wsClient]bool
	mu      sync.RWMutex
}

// newWSClientPool creates a new WebSocket client pool
func newWSClientPool() *wsClientPool {
	return &wsClientPool{
		clients: make(map[*wsClient]bool),
	}
}

// add adds a new client to the pool
func (p *wsClientPool) add(conn *websocket.Conn, id string) *wsClient {
	client := &wsClient{
		conn:         conn,
		writeTimeout: 10 * time.Second,
		lastActivity: time.Now(),
		id:           id,
	}

	p.mu.Lock()
	p.clients[client] = true
	p.mu.Unlock()

	return client
}

// remove removes a client from the pool
func (p *wsClientPool) remove(client *wsClient) {
	p.mu.Lock()
	delete(p.clients, client)
	p.mu.Unlock()
}

// size returns the number of connected clients
func (p *wsClientPool) size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.clients)
}

// broadcast sends a message to all connected clients
// Each write is independent, using per-connection locks
func (p *wsClientPool) broadcast(message interface{}) {
	p.mu.RLock()
	clients := make([]*wsClient, 0, len(p.clients))
	for client := range p.clients {
		clients = append(clients, client)
	}
	p.mu.RUnlock()

	var wg sync.WaitGroup
	for _, client := range clients {
		wg.Add(1)
		go func(c *wsClient) {
			defer wg.Done()
			c.writeJSON(message)
		}(client)
	}
	wg.Wait()
}

// writeJSON writes a JSON message to a specific client
// Uses per-connection lock for thread safety
func (c *wsClient) writeJSON(v interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	if err != nil {
		return err
	}

	err = c.conn.WriteJSON(v)
	if err != nil {
		log.Printf("[ws] Write error to client %s: %v", c.id, err)
		return err
	}

	c.lastActivity = time.Now()
	return nil
}

// writeMessage writes a raw message to a specific client
func (c *wsClient) writeMessage(messageType int, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.conn.SetWriteDeadline(time.Now().Add(c.writeTimeout))
	if err != nil {
		return err
	}

	err = c.conn.WriteMessage(messageType, data)
	if err != nil {
		log.Printf("[ws] Write error to client %s: %v", c.id, err)
		return err
	}

	c.lastActivity = time.Now()
	return nil
}

// close closes the client connection
func (c *wsClient) close() error {
	return c.conn.Close()
}

// ping sends a ping message to check connection health
func (c *wsClient) ping() error {
	return c.writeMessage(websocket.PingMessage, nil)
}

// isStale checks if the client connection is stale (no activity for 5 minutes)
func (c *wsClient) isStale() bool {
	return time.Since(c.lastActivity) > 5*time.Minute
}

// cleanupStale removes stale clients from the pool
func (p *wsClientPool) cleanupStale() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for client := range p.clients {
		if client.isStale() {
			log.Printf("[ws] Removing stale client %s", client.id)
			client.close()
			delete(p.clients, client)
		}
	}
}
