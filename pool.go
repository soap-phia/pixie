package pixie

import (
	"context"
	"sync"
	"time"
)

type Pool struct {
	Factory func(ctx context.Context) (*PixieClient, error)
	Config  *PoolConfig

	Mutex       sync.RWMutex
	Connections []*poolConn

	Closed    chan struct{}
	CloseOnce sync.Once
}

type poolConn struct {
	Pixie    *PixieClient
	Active   int64
	LastUsed time.Time
	Dead     bool
}

type PoolConfig struct {
	MinConns            int
	MaxConns            int
	MaxStreamsPerConn   int
	IdleTimeout         time.Duration
	HealthCheckInterval time.Duration
}

type PoolStats struct {
	TotalConns    int
	ActiveConns   int
	ActiveStreams int
}

type Dialer struct {
	pool    *Pool
	Timeout time.Duration
}

func DefaultPoolConfig() *PoolConfig {
	return &PoolConfig{
		MinConns:            1,
		MaxConns:            10,
		MaxStreamsPerConn:   100,
		IdleTimeout:         5 * time.Minute,
		HealthCheckInterval: 30 * time.Second,
	}
}

func NewPool(Factory func(ctx context.Context) (*PixieClient, error), config *PoolConfig) *Pool {
	if config == nil {
		config = DefaultPoolConfig()
	}

	p := &Pool{
		Factory:     Factory,
		Config:      config,
		Connections: make([]*poolConn, 0, config.MaxConns),
		Closed:      make(chan struct{}),
	}

	go p.Check()

	return p
}

func (p *Pool) OpenStream(ctx context.Context, streamType StreamType, host string, port uint16) (*Stream, error) {
	conn, err := p.GetConn(ctx)
	if err != nil {
		return nil, err
	}

	stream, err := conn.Pixie.OpenStream(ctx, streamType, host, port)
	if err != nil {
		p.Mutex.Lock()
		conn.Active--
		if conn.Active < 0 {
			conn.Active = 0
		}
		p.Mutex.Unlock()
		return nil, err
	}

	return stream, nil
}

func (p *Pool) DialTCP(ctx context.Context, host string, port uint16) (*Stream, error) {
	return p.OpenStream(ctx, strTCP, host, port)
}

func (p *Pool) DialUDP(ctx context.Context, host string, port uint16) (*Stream, error) {
	return p.OpenStream(ctx, strUDP, host, port)
}

func (p *Pool) Close() error {
	p.CloseOnce.Do(func() {
		close(p.Closed)

		p.Mutex.Lock()
		defer p.Mutex.Unlock()

		for _, conn := range p.Connections {
			_ = conn.Pixie.Close()
		}
		p.Connections = nil
	})
	return nil
}

func (p *Pool) Stats() PoolStats {
	p.Mutex.RLock()
	defer p.Mutex.RUnlock()

	stats := PoolStats{
		TotalConns: len(p.Connections),
	}

	for _, conn := range p.Connections {
		if !conn.Dead {
			stats.ActiveConns++
			stats.ActiveStreams += int(conn.Active)
		}
	}

	return stats
}

func (p *Pool) GetConn(ctx context.Context) (*poolConn, error) {
	p.Mutex.Lock()

	var best *poolConn
	for _, conn := range p.Connections {
		if conn.Dead {
			continue
		}
		if conn.Active < int64(p.Config.MaxStreamsPerConn) {
			if best == nil || conn.Active < best.Active {
				best = conn
			}
		}
	}

	if best != nil {
		best.Active++
		best.LastUsed = time.Now()
		p.Mutex.Unlock()
		return best, nil
	}

	if len(p.Connections) >= p.Config.MaxConns {
		p.Mutex.Unlock()
		return nil, eMaxStreams
	}

	p.Mutex.Unlock()

	pixie, err := p.Factory(ctx)
	if err != nil {
		return nil, err
	}

	conn := &poolConn{
		Pixie:    pixie,
		Active:   1,
		LastUsed: time.Now(),
	}

	p.Mutex.Lock()
	p.Connections = append(p.Connections, conn)
	p.Mutex.Unlock()

	go p.MonitorConnection(conn)

	return conn, nil
}

func (p *Pool) MonitorConnection(conn *poolConn) {
	select {
	case <-conn.Pixie.Done():
		p.Mutex.Lock()
		conn.Dead = true
		p.Mutex.Unlock()
	case <-p.Closed:
	}
}

func (p *Pool) Check() {
	ticker := time.NewTicker(p.Config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.Cleanup()
		case <-p.Closed:
			return
		}
	}
}

func (p *Pool) Cleanup() {
	p.Mutex.Lock()
	defer p.Mutex.Unlock()

	now := time.Now()
	var alive []*poolConn

	for _, conn := range p.Connections {
		if conn.Dead {
			continue
		}

		if len(alive) >= p.Config.MinConns &&
			conn.Active == 0 &&
			now.Sub(conn.LastUsed) > p.Config.IdleTimeout {
			_ = conn.Pixie.Close()
			continue
		}

		alive = append(alive, conn)
	}

	p.Connections = alive

	for len(p.Connections) < p.Config.MinConns {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		pixie, err := p.Factory(ctx)
		cancel()

		if err != nil {
			break
		}

		conn := &poolConn{
			Pixie:    pixie,
			LastUsed: time.Now(),
		}
		p.Connections = append(p.Connections, conn)
		go p.MonitorConnection(conn)
	}
}

func NewDialer(pool *Pool) *Dialer {
	return &Dialer{
		pool:    pool,
		Timeout: 30 * time.Second,
	}
}

func (d *Dialer) DialContext(ctx context.Context, network, address string) (*Stream, error) {
	host, port, err := ParseAddress(address)
	if err != nil {
		return nil, err
	}

	if d.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.Timeout)
		defer cancel()
	}

	switch network {
	case "tcp", "tcp4", "tcp6":
		return d.pool.DialTCP(ctx, host, port)
	case "udp", "udp4", "udp6":
		return d.pool.DialUDP(ctx, host, port)
	default:
		return nil, &StreamError{CloseReason: crInvalidInfo}
	}
}

func (d *Dialer) Dial(network, address string) (*Stream, error) {
	return d.DialContext(context.Background(), network, address)
}

func ParseAddress(address string) (string, uint16, error) {
	host, portStr, err := SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}

	var port int
	for _, c := range portStr {
		port = port*10 + int(c-'0')
	}

	return host, uint16(port), nil
}

func SplitHostPort(hostport string) (host, port string, err error) {
	i := len(hostport) - 1
	for i >= 0 && hostport[i] != ':' {
		i--
	}
	if i < 0 {
		return "", "", &StreamError{CloseReason: crInvalidInfo}
	}

	if hostport[0] == '[' {
		end := 1
		for end < len(hostport) && hostport[end] != ']' {
			end++
		}
		if end >= len(hostport) || end+1 >= len(hostport) || hostport[end+1] != ':' {
			return "", "", &StreamError{CloseReason: crInvalidInfo}
		}
		return hostport[1:end], hostport[end+2:], nil
	}

	return hostport[:i], hostport[i+1:], nil
}
