package client

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxConnectionsPerPoolKey = 2
	pooledConnectionMaxIdle  = 25 * time.Second
)

type pooledWebSocket struct {
	connection *websocket.Conn
	returnedAt time.Time
}

type websocketPool struct {
	mutex       sync.Mutex
	connections map[string][]pooledWebSocket
}

func newWebSocketPool() *websocketPool {
	return &websocketPool{connections: make(map[string][]pooledWebSocket)}
}

func (pool *websocketPool) take(key string) *websocket.Conn {
	if pool == nil || key == "" {
		return nil
	}
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	items := pool.connections[key]
	for len(items) > 0 {
		last := len(items) - 1
		item := items[last]
		items = items[:last]
		if time.Since(item.returnedAt) <= pooledConnectionMaxIdle {
			pool.connections[key] = items
			return item.connection
		}
		if item.connection != nil {
			_ = item.connection.Close()
		}
	}
	delete(pool.connections, key)
	return nil
}

func (pool *websocketPool) put(key string, connection *websocket.Conn) {
	if pool == nil || key == "" || connection == nil {
		if connection != nil {
			_ = connection.Close()
		}
		return
	}
	_ = connection.SetReadDeadline(time.Time{})
	_ = connection.SetWriteDeadline(time.Time{})
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	items := pool.connections[key]
	if len(items) >= maxConnectionsPerPoolKey {
		_ = connection.Close()
		return
	}
	pool.connections[key] = append(items, pooledWebSocket{connection: connection, returnedAt: time.Now()})
}

func (pool *websocketPool) count(key string) int {
	if pool == nil || key == "" {
		return 0
	}
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	return len(pool.connections[key])
}

func (pool *websocketPool) discard(connection *websocket.Conn) {
	if connection != nil {
		_ = connection.Close()
	}
}

func (pool *websocketPool) close() {
	if pool == nil {
		return
	}
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	for key, items := range pool.connections {
		for _, item := range items {
			_ = item.connection.Close()
		}
		delete(pool.connections, key)
	}
}
