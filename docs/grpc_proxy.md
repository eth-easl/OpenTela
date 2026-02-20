---
title: "gRPC Forwarding over P2P"
date: "2026-02-20"
tldr: "OpenFabric now natively supports tunneling zero-configuration gRPC traffic over the libp2p network using HTTP/2 Cleartext (h2c)."
---

# gRPC Forwarding over P2P

## Overview
OpenFabric's proxy architecture now includes native support for forwarding gRPC requests (which require HTTP/2) over the existing `libp2p` peer-to-peer network. This allows developers to use standard gRPC clients (like the Python SDK) to interact with remote OpenFabric nodes dynamically.

Prior to this feature, the OpenFabric HTTP proxy was limited to HTTP/1.1 forwarding, which stripped gRPC's HTTP/2 framing, causing connections to fail.

## How it Works
1. **HTTP/2 Cleartext (`h2c`)**: The local OpenFabric node exposes an `h2c` enabled HTTP listener. This allows a local gRPC client to send unencrypted HTTP/2 frames directly to the node without requiring TLS certificates.
2. **Protocol Detection**: When the proxy handler receives a request, it inspects the `Content-Type` header. If it detects `application/grpc`, it classifies the request as gRPC traffic.
3. **P2P Tunneling**: Instead of using the standard HTTP/1.1 `http.Transport`, gRPC requests are routed through a specialized `http2.Transport`. This transport overrides the `DialTLSContext` hook to hijack the dialing sequence, tunneling the HTTP/2 connection directly over a `gostream.Dial` libp2p stream down to the target node.

## Usage Guide

To use gRPC over OpenFabric, simply point your gRPC client to your local OpenFabric node's proxy endpoint.

### 1. Identify the Target
You need the Peer ID of the node you want to communicate with, or the Service Name if using global service routing.

### 2. Configure Your Client
Update your gRPC client to use an insecure channel (since `h2c` is unencrypted locally).

**Python Example:**
```python
import grpc

# 1. Connect to your LOCAL OpenFabric node (assuming default port 8080)
# 2. Append the proxy path to route to the target Peer ID
# Format: <local_ip>:<port>/v1/p2p/<target_peer_id>

LOCAL_NODE = "localhost:8080"
TARGET_PEER_ID = "12D3KooWR..."

channel = grpc.insecure_channel(f"{LOCAL_NODE}/v1/p2p/{TARGET_PEER_ID}")
stub = YourServiceStub(channel)

# Make requests as normal!
response = stub.YourMethod(YourRequest())
```

### 3. Service Forwarding
You can also use the service forwarding endpoints. The proxy will automatically handle load balancing and identity group matching before establishing the `h2c` tunnel.

**Format:**
```python
channel = grpc.insecure_channel(f"{LOCAL_NODE}/v1/service/<service_name>")
```

## Troubleshooting
- **Connection Refused**: Ensure your OpenFabric node is running and the `port` matches your gRPC channel dial address.
- **Protocol Error**: Verify that your client is using an `insecure_channel`. OpenFabric's local listener expects HTTP/2 Cleartext, not TLS.
- **Peer Not Found**: Ensure the target node is connected to the P2P network and accessible from your local node (`GET /v1/dnt/peers`).
