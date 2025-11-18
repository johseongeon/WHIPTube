// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js
// +build !js

// sfu-ws is a many-to-many websocket based SFU
package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/gorilla/websocket"
	"github.com/johseongeon/WHIPTube/webrtc/peer"
	"github.com/johseongeon/WHIPTube/webrtc/track"
	"github.com/johseongeon/WHIPTube/ws"
	"github.com/pion/logging"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// RoomState represents the state of a room
type RoomState struct {
	PeerConnections []peer.PeerConnectionState
	TrackLocals     map[string]*webrtc.TrackLocalStaticRTP
	TrackNames      map[string]string // trackID -> peer name mapping
	StreamNames     map[string]string // streamID -> peer name mapping (fallback)
	Lock            sync.RWMutex
}

// nolint
var (
	// flag.String(name, value, usage)
	_addr = flag.String("addr", ":8080", "http service address")

	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	indexTemplate = &template.Template{}

	// lock for rooms map
	roomsLock sync.RWMutex
	rooms     map[string]*RoomState

	log = logging.NewDefaultLoggerFactory().NewLogger("sfu-ws")
)

func main() {
	// Parse the flags passed to program
	flag.Parse()

	// Init rooms map
	rooms = make(map[string]*RoomState)

	// Read index.html from disk into memory, serve whenever anyone requests /
	indexHTML, err := os.ReadFile("index.html")
	if err != nil {
		panic(err)
	}
	indexTemplate = template.Must(template.New("").Parse(string(indexHTML)))

	// websocket handler
	http.HandleFunc("/websocket", websocketHandler)

	// index.html handler
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Determine WebSocket protocol based on environment.
		protocol := "wss://"

		// for localhost, use ws://
		if strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.0.0.1") {
			protocol = "ws://"
		}

		wsURL := protocol + r.Host + "/websocket"

		if err := indexTemplate.Execute(w, wsURL); err != nil {
			log.Errorf("Failed to parse index template: %v", err)
			return
		}
	})

	// request a keyframe every 3 seconds for all rooms
	go func() {
		for range time.NewTicker(time.Second * 3).C {
			roomsLock.RLock()
			roomList := make([]*RoomState, 0, len(rooms))
			for _, room := range rooms {
				roomList = append(roomList, room)
			}
			roomsLock.RUnlock()

			for _, room := range roomList {
				// DispatchKeyFrame will lock internally, so we don't need to lock here
				room.Lock.RLock()
				peerConnections := room.PeerConnections
				room.Lock.RUnlock()
				peer.DispatchKeyFrame(&room.Lock, peerConnections)
			}
		}
	}()

	// start HTTP server
	if err = http.ListenAndServe(*_addr, nil); err != nil { //nolint: gosec
		log.Errorf("Failed to start http server: %v", err)
	}
}

// getOrCreateRoom gets an existing room or creates a new one
func getOrCreateRoom(roomID string) *RoomState {
	roomsLock.Lock()
	defer roomsLock.Unlock()

	if room, exists := rooms[roomID]; exists {
		return room
	}

	room := &RoomState{
		PeerConnections: make([]peer.PeerConnectionState, 0),
		TrackLocals:     make(map[string]*webrtc.TrackLocalStaticRTP),
		TrackNames:      make(map[string]string),
		StreamNames:     make(map[string]string),
	}
	rooms[roomID] = room
	return room
}

// removePeerFromRoom removes a peer from a room
func removePeerFromRoom(roomID string, peerConnection *webrtc.PeerConnection) {
	roomsLock.RLock()
	room, exists := rooms[roomID]
	roomsLock.RUnlock()

	if !exists {
		return
	}

	room.Lock.Lock()
	defer room.Lock.Unlock()

	for i, pc := range room.PeerConnections {
		if pc.PeerConnection == peerConnection {
			room.PeerConnections = append(room.PeerConnections[:i], room.PeerConnections[i+1:]...)
			break
		}
	}

	// If room is empty, optionally remove it
	if len(room.PeerConnections) == 0 && len(room.TrackLocals) == 0 {
		roomsLock.Lock()
		delete(rooms, roomID)
		roomsLock.Unlock()
	}
}

// Handle incoming websockets.
func websocketHandler(w http.ResponseWriter, r *http.Request) { // nolint
	// Upgrade HTTP request to Websocket
	unsafeConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Errorf("Failed to upgrade HTTP to Websocket: ", err)

		return
	}

	c := &ws.ThreadSafeWriter{unsafeConn, sync.Mutex{}} // nolint

	// When this frame returns close the Websocket
	defer c.Close() //nolint

	c.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.SetPongHandler(func(string) error {
		c.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			<-ticker.C
			if err := c.WriteControl(websocket.PingMessage, []byte("ping"), time.Now().Add(10*time.Second)); err != nil {
				log.Errorf("ping error: %v", err)
				c.Close()
				return
			}
		}
	}()

	var roomID string
	var userName string
	var room *RoomState

	// Wait for room_id and name from client
	message := &ws.WebsocketMessage{}
	_, raw, err := c.ReadMessage()
	if err != nil {
		log.Errorf("Failed to read room_id message: %v", err)
		return
	}

	log.Infof("Got initial message: %s", raw)

	if err := json.Unmarshal(raw, &message); err != nil {
		log.Errorf("Failed to unmarshal json to message: %v", err)
		return
	}

	if message.Event != "join" {
		log.Errorf("Expected 'join' event with room_id, got: %s", message.Event)
		return
	}

	// Parse join data as JSON: {roomId: "...", name: "..."}
	var joinData struct {
		RoomID string `json:"roomId"`
		Name   string `json:"name"`
	}
	if err := json.Unmarshal([]byte(message.Data), &joinData); err != nil {
		log.Errorf("Failed to unmarshal join data: %v", err)
		return
	}

	roomID = joinData.RoomID
	userName = joinData.Name

	if roomID == "" {
		log.Errorf("room_id is empty")
		return
	}

	if userName == "" {
		userName = "Anonymous"
	}

	log.Infof("Client joining room: %s with name: %s", roomID, userName)
	room = getOrCreateRoom(roomID)

	// Create new PeerConnection
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{
				URLs: []string{"stun:stun.l.google.com:19302"},
			},
		}})
	if err != nil {
		log.Errorf("Failed to creates a PeerConnection: %v", err)

		return
	}

	// When this frame returns close the PeerConnection
	defer func() {
		peerConnection.Close()
		removePeerFromRoom(roomID, peerConnection)
	}()

	// Accept one audio  track incoming
	for _, typ := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio} {
		if _, err := peerConnection.AddTransceiverFromKind(typ, webrtc.RTPTransceiverInit{
			Direction: webrtc.RTPTransceiverDirectionRecvonly,
		}); err != nil {
			log.Errorf("Failed to add transceiver: %v", err)

			return
		}
	}

	// Add our new PeerConnection to room
	room.Lock.Lock()
	room.PeerConnections = append(room.PeerConnections, peer.PeerConnectionState{
		PeerConnection: peerConnection,
		Websocket:      c,
		Name:           userName})
	room.Lock.Unlock()

	// Trickle ICE. Emit server candidate to client
	peerConnection.OnICECandidate(func(i *webrtc.ICECandidate) {
		if i == nil {
			return
		}
		// If you are serializing a candidate make sure to use ToJSON
		// Using Marshal will result in errors around `sdpMid`
		candidateString, err := json.Marshal(i.ToJSON())
		if err != nil {
			log.Errorf("Failed to marshal candidate to json: %v", err)

			return
		}

		log.Infof("Send candidate to client: %s", candidateString)

		if writeErr := c.WriteJSON(&ws.WebsocketMessage{
			Event: "candidate",
			Data:  string(candidateString),
		}); writeErr != nil {
			log.Errorf("Failed to write JSON: %v", writeErr)
		}
	})

	// If PeerConnection is closed remove it from room
	peerConnection.OnConnectionStateChange(func(p webrtc.PeerConnectionState) {
		log.Infof("Connection state change: %s", p)

		switch p {
		case webrtc.PeerConnectionStateFailed:
			if err := peerConnection.Close(); err != nil {
				log.Errorf("Failed to close PeerConnection: %v", err)
			}
		case webrtc.PeerConnectionStateClosed:
			peer.SignalPeerConnections(&room.Lock, room.TrackLocals, room.PeerConnections, room.TrackNames, room.StreamNames)
		default:
		}
	})

	peerConnection.OnTrack(func(t *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		log.Infof("Got remote track: Kind=%s, ID=%s, PayloadType=%d", t.Kind(), t.ID(), t.PayloadType())

		// Find the peer name for this track
		var trackOwnerName string
		room.Lock.RLock()
		for _, pc := range room.PeerConnections {
			if pc.PeerConnection == peerConnection {
				trackOwnerName = pc.Name
				break
			}
		}
		room.Lock.RUnlock()

		// Map track ID and stream ID to peer name
		trackID := t.ID()
		streamID := t.StreamID()
		room.Lock.Lock()
		room.TrackNames[trackID] = trackOwnerName
		room.StreamNames[streamID] = trackOwnerName
		log.Infof("Mapped track ID %s and stream ID %s to peer name: %s", trackID, streamID, trackOwnerName)
		room.Lock.Unlock()

		// Create a track to fan out our incoming audio to all peers in the room
		trackLocal := track.AddTrack(t, &room.Lock, room.TrackLocals, room.PeerConnections, room.TrackNames, room.StreamNames)

		// Also map the local track ID (in case it's different)
		localTrackID := trackLocal.ID()
		if localTrackID != trackID {
			room.Lock.Lock()
			room.TrackNames[localTrackID] = trackOwnerName
			log.Infof("Also mapped local track ID %s to peer name: %s", localTrackID, trackOwnerName)
			room.Lock.Unlock()
		}

		defer func() {
			room.Lock.Lock()
			delete(room.TrackNames, t.ID())
			delete(room.StreamNames, streamID)
			room.Lock.Unlock()
			track.RemoveTrack(trackLocal, &room.Lock, room.TrackLocals, room.PeerConnections, room.TrackNames, room.StreamNames)
		}()

		buf := make([]byte, 1500)
		rtpPkt := &rtp.Packet{}

		for {
			i, _, err := t.Read(buf)
			if err != nil {
				return
			}

			if err = rtpPkt.Unmarshal(buf[:i]); err != nil {
				log.Errorf("Failed to unmarshal incoming RTP packet: %v", err)

				return
			}

			rtpPkt.Extension = false
			rtpPkt.Extensions = nil

			if err = trackLocal.WriteRTP(rtpPkt); err != nil {
				return
			}
		}
	})

	peerConnection.OnICEConnectionStateChange(func(is webrtc.ICEConnectionState) {
		log.Infof("ICE connection state changed: %s", is)
	})

	// Signal for the new PeerConnection
	peer.SignalPeerConnections(&room.Lock, room.TrackLocals, room.PeerConnections, room.TrackNames, room.StreamNames)

	// Continue reading messages
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			log.Errorf("Failed to read message: %v", err)

			return
		}

		log.Infof("Got message: %s", raw)

		if err := json.Unmarshal(raw, &message); err != nil {
			log.Errorf("Failed to unmarshal json to message: %v", err)

			return
		}

		switch message.Event {
		case "candidate":
			candidate := webrtc.ICECandidateInit{}
			if err := json.Unmarshal([]byte(message.Data), &candidate); err != nil {
				log.Errorf("Failed to unmarshal json to candidate: %v", err)

				return
			}

			log.Infof("Got candidate: %v", candidate)

			if err := peerConnection.AddICECandidate(candidate); err != nil {
				log.Errorf("Failed to add ICE candidate: %v", err)

				return
			}
		case "answer":
			answer := webrtc.SessionDescription{}
			if err := json.Unmarshal([]byte(message.Data), &answer); err != nil {
				log.Errorf("Failed to unmarshal json to answer: %v", err)

				return
			}

			log.Infof("Got answer: %v", answer)

			if err := peerConnection.SetRemoteDescription(answer); err != nil {
				log.Errorf("Failed to set remote description: %v", err)

				return
			}
		default:
			log.Errorf("unknown message: %+v", message)
		}
	}
}
