package peer

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/johseongeon/WHIPTube/ws"
	"github.com/pion/logging"
	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

var log = logging.NewDefaultLoggerFactory().NewLogger("sfu-ws")

// dispatchKeyFrame sends a keyframe to all PeerConnections, used everytime a new user joins the call.
func DispatchKeyFrame(listLock *sync.RWMutex, peerConnections []PeerConnectionState) {
	listLock.Lock()
	defer listLock.Unlock()

	for i := range peerConnections {
		for _, receiver := range peerConnections[i].PeerConnection.GetReceivers() {
			if receiver.Track() == nil {
				continue
			}

			_ = peerConnections[i].PeerConnection.WriteRTCP([]rtcp.Packet{
				&rtcp.PictureLossIndication{
					MediaSSRC: uint32(receiver.Track().SSRC()),
				},
			})
		}
	}
}

// signalPeerConnections updates each PeerConnection so that it is getting all the expected media tracks.
func SignalPeerConnections(listLock *sync.RWMutex, trackLocals map[string]*webrtc.TrackLocalStaticRTP, peerConnections []PeerConnectionState, trackNames map[string]string, streamNames map[string]string) { // nolint
	listLock.Lock()
	defer func() {
		listLock.Unlock()
		DispatchKeyFrame(listLock, peerConnections)
	}()

	attemptSync := func() (tryAgain bool) {
		for i := range peerConnections {
			if peerConnections[i].PeerConnection.ConnectionState() == webrtc.PeerConnectionStateClosed {
				peerConnections = append(peerConnections[:i], peerConnections[i+1:]...)

				return true // We modified the slice, start from the beginning
			}

			// map of sender we already are seanding, so we don't double send
			existingSenders := map[string]bool{}

			for _, sender := range peerConnections[i].PeerConnection.GetSenders() {
				if sender.Track() == nil {
					continue
				}

				existingSenders[sender.Track().ID()] = true

				// If we have a RTPSender that doesn't map to a existing track remove and signal
				if _, ok := trackLocals[sender.Track().ID()]; !ok {
					if err := peerConnections[i].PeerConnection.RemoveTrack(sender); err != nil {

						return true
					}
				}
			}

			// Don't receive videos we are sending, make sure we don't have loopback
			for _, receiver := range peerConnections[i].PeerConnection.GetReceivers() {
				if receiver.Track() == nil {
					continue
				}

				existingSenders[receiver.Track().ID()] = true
			}

			// Add all track we aren't sending yet to the PeerConnection
			for trackID := range trackLocals {
				if _, ok := existingSenders[trackID]; !ok {
					if _, err := peerConnections[i].PeerConnection.AddTrack(trackLocals[trackID]); err != nil {
						log.Errorf("Failed to add track to PeerConnection: %v", err)
						return true
					}
				}
			}

			offer, err := peerConnections[i].PeerConnection.CreateOffer(nil)
			if err != nil {
				log.Errorf("Failed to create offer: %v", err)
				return true
			}

			if err = peerConnections[i].PeerConnection.SetLocalDescription(offer); err != nil {
				log.Errorf("Failed to set local description: %v", err)
				return true
			}

			offerString, err := json.Marshal(offer)
			if err != nil {
				log.Errorf("Failed to marshal offer to json: %v", err)
				return true
			}

			// Create offer message with track names and stream names
			offerData := map[string]interface{}{
				"offer":       json.RawMessage(offerString),
				"trackNames":  trackNames,
				"streamNames": streamNames,
			}
			offerDataString, err := json.Marshal(offerData)
			if err != nil {
				log.Errorf("Failed to marshal offer data: %v", err)
				return true
			}

			log.Errorf("Send offer to client with trackNames: %v", trackNames)

			if err = peerConnections[i].Websocket.WriteJSON(&ws.WebsocketMessage{
				Event: "offer",
				Data:  string(offerDataString),
			}); err != nil {
				return true
			}
		}

		return tryAgain
	}

	for syncAttempt := 0; ; syncAttempt++ {
		if syncAttempt == 25 {
			// Release the lock and attempt a sync in 3 seconds. We might be blocking a RemoveTrack or AddTrack
			go func() {
				time.Sleep(time.Second * 3)
				SignalPeerConnections(listLock, trackLocals, peerConnections, trackNames, streamNames)
			}()

			return
		}

		if !attemptSync() {
			break
		}
	}
}
