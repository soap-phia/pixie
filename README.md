# Pixie

A high-performance Wisp v2 library written in Go.

## What is Pixie?

Pixie is a Go library implementing the [Wisp protocol](https://github.com/MercuryWorkshop/wisp-protocol) for multiplexing TCP/UDP streams over a single WebSocket connection. It's optimized for high throughput and low latency, with built-in support for:

- Wisp v2 with automatic v1 fallback
- Per-stream flow control
- Protocol extensions (UDP, password auth, certificate auth, MOTD, custom)
- Batched writes for reduced syscall overhead
- Connection metrics
- DNS caching

## Implementations

- [mrrowisp](https://github.com/soap-phia/mrrowisp) - A Wisp protocol server

## Improvements over wisp-mux

Pixie is written from scratch in Go with the goal of improving on / adding to the Rust `wisp-mux` crate:

| Feature | wisp-mux (Rust) | Pixie (Go) |
|---------|-----------------|------------|
| Transport | Trait-based | Interface-based with adapters |
| Write batching | Per-packet | Batched |
| Flow control | Credit-based via Continue | Credit-based via batched Continue flushing |
| Extension system | Builder pattern | Interface + registry |
| Memory | Zero-copy where possible | Pooled buffers for hot paths |
| Read loop | Async stream | Dedicated goroutine with fast-path inlining |

Key differences:

1. **Batched writes**: Pixie's `BatchTransport` collects pending writes and flushes them in a single vectored I/O call, reducing syscalls under load.

2. **Inlined fast paths**: The read loop has inlined handling for DATA and CONTINUE packets (the hot path), avoiding allocation and channel overhead for 99% of traffic.

3. **Pooled allocations**: Packet buffers, continue packets, and close packets use `sync.Pool` to avoid GC pressure.

4. **Framed transport**: Optional raw WebSocket framing for servers that want to bypass higher-level WebSocket libraries entirely.

5. **Stream handler callback**: Servers can register `OnStreamOpen` to handle streams inline rather than accepting from a channel.

## Installation

```bash
go get github.com/soap-phia/pixie
```

## Quick Start

### Client

```go
package main

import (
    "context"
    "io"
    "log"
    "os"

    "github.com/gorilla/websocket"
    "github.com/soap-phia/pixie"
)

func main() {
    ctx := context.Background()

    // Connect to Wisp server
    conn, _, err := websocket.DefaultDialer.Dial("ws://localhost:8080/", nil)
    if err != nil {
        log.Fatal(err)
    }

    // Create transport adapter
    transport := pixie.NewGorillaTransport(conn)

    // Create client with optional extensions
    client, err := pixie.NewPixie(ctx, transport,
        pixie.WithExtensions(&pixie.UDPExtension{}),
        pixie.WithBufferSize(128),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // Check if connection was downgraded to v1
    if client.WasDowngraded() {
        log.Println("Connected with Wisp v1 (downgraded)")
    }

    // Open a TCP stream
    stream, err := client.DialTCP(ctx, "example.com", 80)
    if err != nil {
        log.Fatal(err)
    }
    defer stream.Close()

    // Use like a net.Conn
    stream.Write([]byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"))
    io.Copy(os.Stdout, stream)
}
```

### Client Patterns

The client examples below show common patterns used across Wisp implementations (wisp-mux, wisp-js).

#### Version Negotiation and Downgrade

Pixie automatically negotiates with the server:
- Sends v2 handshake if extensions are configured
- Falls back to v1 if the server only supports v1
- On v1 fallback, UDP is automatically enabled (v1 assumed UDP support)

```go
client, err := pixie.NewPixie(ctx, transport,
    pixie.WithExtensions(&pixie.UDPExtension{}, &pixie.MOTDExtension{}),
)
if err != nil {
    // Handshake failed - incompatible version, auth rejected, etc.
    log.Fatal(err)
}

if client.WasDowngraded() {
    // Server only supports v1 - extensions won't work except UDP
    log.Println("Warning: Connected with Wisp v1, some features unavailable")
}

// Check negotiated buffer size
log.Printf("Server buffer size: %d packets", client.GetBufferSize())
```

#### Checking Extension Support

After handshake, only mutually supported extensions are active:

```go
// Check if specific extension was negotiated
if client.HasExtension(pixie.UDPExtensionID) {
    stream, _ := client.DialUDP(ctx, "8.8.8.8", 53)
    // ...
}

// Get all negotiated extensions
for _, ext := range client.GetExtensions() {
    switch e := ext.(type) {
    case *pixie.MOTDExtension:
        log.Println("Server MOTD:", e.Message)
    case *pixie.UDPExtension:
        log.Println("UDP streams available")
    }
}

// Attempting UDP without extension support returns an error
if !client.HasExtension(pixie.UDPExtensionID) {
    _, err := client.DialUDP(ctx, "8.8.8.8", 53)
    // err: "pixie: invalid extension: UDP extension required"
}
```

#### Stream as net.Conn

Streams implement the standard `net.Conn` interface, enabling use with any Go networking code:

```go
stream, err := client.DialTCP(ctx, "example.com", 443)
if err != nil {
    log.Fatal(err)
}
defer stream.Close()

// Upgrade to TLS
tlsConn := tls.Client(stream, &tls.Config{ServerName: "example.com"})
if err := tlsConn.Handshake(); err != nil {
    log.Fatal(err)
}

// Use with http.Client
httpClient := &http.Client{
    Transport: &http.Transport{
        DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
            host, portStr, _ := net.SplitHostPort(addr)
            port, _ := strconv.ParseUint(portStr, 10, 16)
            return client.DialTCP(ctx, host, uint16(port))
        },
    },
}
resp, _ := httpClient.Get("http://example.com/")
```

#### Error Handling

```go
// Connection errors
client, err := pixie.NewPixie(ctx, transport, pixie.WithExtensions(...))
if err != nil {
    if errors.Is(err, pixie.ErrPixieClosed) {
        // Transport was closed during handshake
    }
    // Other errors: version mismatch, auth failure, etc.
    log.Fatal(err)
}

// Stream errors
stream, err := client.DialTCP(ctx, "example.com", 80)
if err != nil {
    if errors.Is(err, pixie.ErrPixieClosed) {
        // Client was closed
    }
    log.Fatal(err)
}

// Read/Write errors
n, err := stream.Read(buf)
if err != nil {
    if err == io.EOF {
        // Stream closed normally
    } else if se, ok := err.(*pixie.StreamError); ok {
        // Stream closed with reason
        switch se.CloseReason {
        case pixie.CrUnreachable:
            log.Println("Target unreachable")
        case pixie.CrRefused:
            log.Println("Connection refused")
        case pixie.CrConnTimeout:
            log.Println("Connection timed out")
        case pixie.CrBlockedAddr:
            log.Println("Address blocked by server policy")
        case pixie.CrThrottled:
            log.Println("Rate limited")
        }
    }
}

// Timeouts with deadlines
stream.SetDeadline(time.Now().Add(30 * time.Second))
stream.SetReadDeadline(time.Now().Add(10 * time.Second))
stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
```

#### Flow Control Behavior

Writes may block when the server's buffer is full:

```go
// Flow control is automatic - writes block when buffer exhausted
stream, _ := client.DialTCP(ctx, "example.com", 80)

// This may block if server hasn't sent CONTINUE packets
n, err := stream.Write(largePayload)
if err != nil {
    // Context canceled, deadline exceeded, or stream closed
}

// Use write deadline to avoid indefinite blocking
stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
n, err = stream.Write(data)
if err != nil {
    // Could be timeout: &net.OpError{Op: "write", Err: context.DeadlineExceeded}
}
```

#### Graceful Shutdown

```go
// Wait for client to close (e.g., server disconnected)
select {
case <-client.Done():
    log.Println("Connection closed")
case <-ctx.Done():
    log.Println("Context canceled")
}

// Close with specific reason
client.CloseWithReason(pixie.CrVoluntary)

// Or just close normally
client.Close()
```

### Server

```go
package main

import (
    "context"
    "io"
    "log"
    "net"
    "net/http"

    "github.com/gorilla/websocket"
    "github.com/soap-phia/pixie"
)

var upgrader = websocket.Upgrader{
    CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
    http.HandleFunc("/", handleWs)
    log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleWs(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        return
    }

    ctx := context.Background()
    transport := pixie.NewGorillaTransport(conn)

    server, err := pixie.NewPixieServer(ctx, transport,
        pixie.WithServerExtensions(&pixie.UDPExtension{}),
        pixie.WithServerBufferSize(128),
    )
    if err != nil {
        conn.Close()
        return
    }
    defer server.Close()

    // Accept and handle streams
    for {
        stream, connect, err := server.AcceptStream(ctx)
        if err != nil {
            break
        }

        go handleStream(stream, connect)
    }
}

func handleStream(stream *pixie.Stream, connect *pixie.ConnectPacket) {
    defer stream.Close()

    // Connect to target
    target := net.JoinHostPort(stream.GetHost(), fmt.Sprint(stream.GetPort()))
    conn, err := net.Dial("tcp", target)
    if err != nil {
        stream.CloseWithReason(pixie.CrUnreachable)
        return
    }
    defer conn.Close()

    // Bidirectional copy
    go io.Copy(conn, stream)
    io.Copy(stream, conn)
}
```

### Server with Stream Handler

For better performance, use the stream handler callback instead of `AcceptStream`:

```go
server, err := pixie.NewPixieServer(ctx, transport,
    pixie.WithStreamHandler(func(ctx context.Context, stream *pixie.Stream, connect *pixie.ConnectPacket) error {
        // Handle stream directly - runs in its own goroutine
        go handleStream(stream, connect)
        return nil
    }),
)
```

## Internal Transports

Pixie uses an internal transport abstraction for WebSocket handling. This is separate from bare transports (which are client-side browser APIs).

Implement the `Transport` interface or use a built-in adapter:

```go
type Transport interface {
    io.Closer
    ReadMessage(ctx context.Context) ([]byte, error)
    WriteMessage(ctx context.Context, data []byte) error
}
```

### Built-in Adapters

Pixie provides adapters for three popular Go WebSocket libraries:

| Library | Adapter | Best For |
|---------|---------|----------|
| [gorilla/websocket](https://github.com/gorilla/websocket) | `NewGorillaTransport` | General use, most popular |
| [nhooyr.io/websocket](https://github.com/nhooyr/websocket) | `NewNhooyrTransport` | Context-native cancellation |
| [gobwas/ws](https://github.com/gobwas/ws) | `NewGobwasTransport` | Maximum performance, zero-alloc |

```go
// Gorilla WebSocket (Recommended)
// - Most widely used Go WebSocket library
// - Blocking API with deadline-based timeouts
// - Handles WebSocket framing internally
import "github.com/gorilla/websocket"
conn, _ := upgrader.Upgrade(w, r, nil)
transport := pixie.NewGorillaTransport(conn)

// nhooyr.io/websocket
// - Modern, context-first API design
// - Thread-safe without external synchronization
// - Clean cancellation via context
import "nhooyr.io/websocket"
conn, _ := websocket.Accept(w, r, nil)
transport := pixie.NewNhooyrTransport(conn)

// gobwas/ws
// - Low-level, zero-allocation library
// - Uses length-prefixed framing (not standard WebSocket frames)
// - Supports vectored I/O via net.Buffers for reduced syscalls
// - Best for high-throughput scenarios where you control both ends
import "github.com/gobwas/ws"
conn, _, _, _ := ws.UpgradeHTTP(r, w)
transport := pixie.NewGobwasTransport(conn)

// Channel-based (for testing)
client, server := pixie.NewChannelTransportPair()
```

#### Choosing an Adapter

**Gorilla** is the default choice for most applications. It's battle-tested, well-documented, and works with standard WebSocket clients.

**nhooyr** is preferred when you need clean context-based cancellation or are already using it elsewhere in your codebase. Its API is more idiomatic for modern Go.

**gobwas** is for performance-critical deployments where you control both client and server. Note that the Pixie adapter uses **4-byte length-prefixed framing** rather than standard WebSocket frames, which reduces overhead but requires a compatible client. This is ideal for:
- Server-to-server Wisp proxying
- Custom clients that bypass browser WebSocket APIs
- Scenarios where WebSocket framing overhead matters

### Batched Transport

For high-throughput servers, use `BatchTransport` to batch writes:

```go
// Wraps any transport with write batching
bt := pixie.NewBatchTransport(transport, 4096)

// Or for raw socket framing (bypasses WebSocket library)
bt := pixie.NewFramedTransport(netConn, 8192)
```

### Custom Transport

```go
type MyTransport struct {
    conn *websocket.Conn
}

func (t *MyTransport) ReadMessage(ctx context.Context) ([]byte, error) {
    _, data, err := t.conn.ReadMessage()
    return data, err
}

func (t *MyTransport) WriteMessage(ctx context.Context, data []byte) error {
    return t.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (t *MyTransport) Close() error {
    return t.conn.Close()
}
```

## Extensions

### UDP Extension

Enables UDP stream support:

```go
// Client
client, _ := pixie.NewPixie(ctx, transport,
    pixie.WithExtensions(&pixie.UDPExtension{}),
)

// Open UDP stream
stream, err := client.DialUDP(ctx, "8.8.8.8", 53)
```

### Password Authentication

```go
// Server
server, _ := pixie.NewPixieServer(ctx, transport,
    pixie.WithServerExtensions(&pixie.PasswordExtension{
        Required: true,
        Validator: func(username, password string) bool {
            return username == "admin" && password == "secret"
        },
    }),
)

// Client
client, _ := pixie.NewPixie(ctx, transport,
    pixie.WithExtensions(&pixie.PasswordExtension{
        Username: "admin",
        Password: "secret",
    }),
)
```

### Certificate Authentication

Ed25519 challenge-response authentication:

```go
// Server
server, _ := pixie.NewPixieServer(ctx, transport,
    pixie.WithServerExtensions(&pixie.CertAuthExtension{
        Required:            true,
        SupportedAlgorithms: pixie.SigAlgoEd25519,
        Validator: func(publicKeyHash [32]byte) ed25519.PublicKey {
            // Return the public key if authorized, nil otherwise
            if key, ok := authorizedKeys[publicKeyHash]; ok {
                return key
            }
            return nil
        },
    }),
)

// Client
client, _ := pixie.NewPixie(ctx, transport,
    pixie.WithExtensions(&pixie.CertAuthExtension{
        PrivateKey: privateKey, // ed25519.PrivateKey
    }),
)
```

### MOTD Extension

Server message of the day:

```go
// Server
server, _ := pixie.NewPixieServer(ctx, transport,
    pixie.WithServerExtensions(&pixie.MOTDExtension{
        Message: "Welcome to my Wisp server!",
    }),
)

// Client - read MOTD after connect
for _, ext := range client.GetExtensions() {
    if motd, ok := ext.(*pixie.MOTDExtension); ok {
        fmt.Println("MOTD:", motd.Message)
    }
}
```

### Custom Extensions

```go
type MyExtension struct {
    Data string
}

func (e *MyExtension) ID() uint8 { return 0x10 } // Use 0x10-0xEF for custom

func (e *MyExtension) Encode(role pixie.Role) []byte {
    return []byte(e.Data)
}

func (e *MyExtension) Decode(data []byte, role pixie.Role) error {
    e.Data = string(data)
    return nil
}

func (e *MyExtension) HandleHandshake(ctx context.Context, transport pixie.Transport, role pixie.Role) error {
    // Custom handshake logic
    return nil
}

func (e *MyExtension) SupportedPacketTypes() []pixie.PacketType {
    return []pixie.PacketType{0xF0} // Custom packet types 0xF0-0xFF
}

func (e *MyExtension) PacketHandler(ctx context.Context, pt pixie.PacketType, data []byte, transport pixie.Transport) error {
    // Handle custom packets
    return nil
}

func (e *MyExtension) CongestionStreamTypes() []pixie.StreamType {
    return nil // Return stream types that need flow control
}

func (e *MyExtension) Clone() pixie.Extension {
    return &MyExtension{Data: e.Data}
}
```

## Flow Control

Pixie implements credit-based flow control to prevent buffer bloat:

- **Server side**: Sends `Continue` packets when buffer space frees up
- **Client side**: Tracks available buffer and waits when exhausted

Flow control is automatic and batched for efficiency. Configure the buffer size:

```go
// Client
pixie.WithBufferSize(256) // 256 packet credits

// Server
pixie.WithServerBufferSize(256)
```

## Metrics

Enable metrics collection:

```go
client, _ := pixie.NewPixie(ctx, transport, pixie.WithMetrics())

// Later...
metrics := client.GetMetrics()
snapshot := metrics.Snapshot()
fmt.Printf("Streams: %d opened, %d closed\n", snapshot.StreamsOpened, snapshot.StreamsClosed)
fmt.Printf("Traffic: %d bytes sent, %d received\n", snapshot.BytesSent, snapshot.BytesReceived)
```

## DNS Caching

Built-in DNS cache for repeated lookups:

```go
cache := pixie.NewDNSCache(nil, 5*time.Minute)

// Use in your stream handler
resolved, err := cache.LookupHost(ctx, hostname)

// Maintenance
cache.Prune() // Remove expired entries
cache.Clear() // Clear all entries
```

## Stream Types

```go
pixie.StrTCP // TCP stream (0x01)
pixie.StrUDP // UDP stream (0x02) - requires UDP extension
pixie.StrPTY // PTY stream (0x03) - for terminal multiplexing
```

## Close Reasons

When closing streams, use appropriate reasons:

```go
stream.CloseWithReason(pixie.CrVoluntary)      // Normal close
stream.CloseWithReason(pixie.CrUnreachable)    // Target unreachable
stream.CloseWithReason(pixie.CrRefused)        // Connection refused
stream.CloseWithReason(pixie.CrConnTimeout)    // Connection timeout
stream.CloseWithReason(pixie.CrBlockedAddr)    // Address blocked by policy
stream.CloseWithReason(pixie.CrThrottled)      // Rate limited
```

## Example: mrrowisp

[mrrowisp](https://github.com/soap-phia/mrrowisp) is a full-featured Wisp server built with Pixie, demonstrating:

- WebSocket upgrade handling
- TCP/UDP proxying
- PTY multiplexing (Twisp)
- Password and certificate authentication
- DNS caching
- IP allowlisting
- TLS support
- Configurable via JSON

See the mrrowisp source for a production-ready example.

## Configuration

```go
config := pixie.DefaultConfig()
config.BufferSize = 128           // Flow control buffer size
config.StreamChannelSize = 64     // Per-stream receive buffer
config.PacketChannelSize = 2048   // Main packet channel size
config.MaxStreams = 0             // 0 = unlimited
config.EnableMetrics = true
config.ForceV1 = false            // Force Wisp v1 mode
```

## Thread Safety

- `PixieClient` and `PixieServer` are safe for concurrent use
- `Stream` read/write can be called from different goroutines
- Extension access is protected by `RWMutex`
- Metrics use atomic counters

## Credits
- [soap phia](https://github.com/soap-phia/) - writing pixie
- [rebecca](https://github.com/rebeccaheartz69/) - helping with wisp v2 and extensions
- [EV's Cousin](https://claude.ai) - Writing this README