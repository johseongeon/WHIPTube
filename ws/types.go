package ws

import (
	"sync"

	"github.com/gorilla/websocket"
)

type WebsocketMessage struct {
	Event string `json:"event"`
	Data  string `json:"data"`
}

// Helper to make Gorilla Websockets threadsafe.
type ThreadSafeWriter struct {
	*websocket.Conn
	sync.Mutex
}
