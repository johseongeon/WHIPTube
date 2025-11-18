<h1 align="center">
  <a href="https://pion.ly"><img width="640" height="400" alt="Image" src="https://github.com/user-attachments/assets/93b96c83-5695-4117-a768-926c360ca967" /></a>
  <br>
  Multi-Room Voice Chatting based on WebRTC
  <br>
</h1>
<h4 align="center">A pure Go implementation of the WebRTC API</h4>
<p align="center">
  <a href="https://pion.ly"><img src="https://img.shields.io/badge/pion-webrtc-gray.svg?longCache=true&colorB=brightgreen" alt="Pion WebRTC"></a>
  <a href="https://pkg.go.dev/github.com/pion/webrtc/v4"><img src="https://pkg.go.dev/badge/github.com/pion/webrtc/v4.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/johseongeon/VoiceChat"><img src="https://goreportcard.com/badge/github.com/johseongeon/VoiceChat" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
  </p>
<br>

---

##  Reference

### • Pion SFU-WS Example
https://github.com/pion/example-webrtc-applications/tree/master/sfu-ws

### • WebRTC for the Curious
https://webrtcforthecurious.com

---
# SFU(Selective Forwarding Unit)

<img width="492" height="134" alt="Image" src="https://github.com/user-attachments/assets/a5a7ca16-6c3c-43b7-8080-9a38ef9e15b5" />

An SFU implements a client/server topology, instead of P2P. Each WebRTC peer connects to the SFU and uploads its media. The SFU then forwards this media out to each connected client.

With an SFU each WebRTC Agent only has to encode and upload their video once. The burden of distributing it to all the viewers is on the SFU. Connectivity with an SFU is much easier than P2P as well. You can run an SFU on a world routable address, making it much easier for clients to connect. You don’t need to worry about NAT Mappings. You do still need to make sure your SFU is available via TCP (either via ICE-TCP or TURN).

---

# SFU-WS

sfu-ws is a many-to-many websocket based SFU. This is a more advanced version of [broadcast](https://github.com/pion/webrtc/tree/master/examples/broadcast)
and demonstrates the following features.

* Trickle ICE
* Re-negotiation
* Basic RTCP
* Multiple inbound/outbound tracks per PeerConnection
* No codec restriction per call. You can have H264 and VP8 in the same conference.
* Support for multiple browsers

---

# Getting Started

## 0. Before Getting Started

### 1. You must open the required UDP ports in the production env.

- EC2 Security Group, for example

### 2. Media transmission in the browser, such as audio, requires HTTPS.

### 3. Nginx configuration is required for proper routing and secure deployment.

### Nginx config (example)

```
http {
    tcp_nopush on;
    tcp_nodelay on;
    keepalive_timeout 3600s;
    proxy_socket_keepalive on;
    ...
}

server {
    server_name {your-domain};

    location / {
        proxy_pass http://127.0.0.1:8080;

        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;

        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";

        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
        send_timeout 3600s;
        proxy_connect_timeout 3600s;

        # Real-time streaming
        proxy_buffering off;

        # nginx keepalive
        keepalive_timeout 3600s;
    }

    https settings # managed by Certbot

}
```

## 1. Git Clone

```
git clone https://github.com/johseongeon/VoiceChat
```

## 2. Build Image

```
cd VoiceChat
docker build -t voice_chat .
```

## 3. Run Container

```
docker run -d --name voice_chat --network host --restart=always voice_chat
```
