package client

import (
	"testing"
	"time"
)

func TestWebSocketPoolSeparatesKeysAndExpiresIdleEntries(t *testing.T) {
	pool := newWebSocketPool()
	pool.connections["account-a|tenant|proxy-1"] = []pooledWebSocket{{returnedAt: time.Now()}}
	pool.connections["account-a|tenant|proxy-2"] = []pooledWebSocket{{returnedAt: time.Now().Add(-2 * pooledConnectionMaxIdle)}}
	if len(pool.connections["account-a|tenant|proxy-1"]) != 1 {
		t.Fatal("first isolation key was not retained")
	}
	if connection := pool.take("account-a|tenant|proxy-2"); connection != nil {
		t.Fatal("expired connection was returned")
	}
	if _, exists := pool.connections["account-a|tenant|proxy-2"]; exists {
		t.Fatal("expired isolation key was not removed")
	}
}

func TestWebSocketPoolCountRemainsKeyIsolated(t *testing.T) {
	pool := newWebSocketPool()
	pool.connections["token-a|oid-a|tid-a|1"] = []pooledWebSocket{{returnedAt: time.Now()}}
	pool.connections["token-b|oid-b|tid-b|1"] = []pooledWebSocket{{returnedAt: time.Now()}, {returnedAt: time.Now()}}
	if got := pool.count("token-a|oid-a|tid-a|1"); got != 1 {
		t.Fatalf("account A count = %d, want 1", got)
	}
	if got := pool.count("token-b|oid-b|tid-b|1"); got != 2 {
		t.Fatalf("account B count = %d, want 2", got)
	}
}
