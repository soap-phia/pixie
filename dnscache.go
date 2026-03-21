package pixie

import (
	"context"
	"net"
	"sync"
	"time"
)

type dnsEntry struct {
	Ips       []net.IPAddr
	ExpiresAt time.Time
}

type DNSCache struct {
	Resolver *net.Resolver
	TTL      time.Duration

	Mutex sync.RWMutex
	Cache map[string]dnsEntry
}

func NewDNSCache(resolver *net.Resolver, ttl time.Duration) *DNSCache {
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	return &DNSCache{
		Resolver: resolver,
		TTL:      ttl,
		Cache:    make(map[string]dnsEntry),
	}
}

func (d *DNSCache) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	now := time.Now()

	d.Mutex.RLock()
	entry, ok := d.Cache[host]
	d.Mutex.RUnlock()

	if ok && now.Before(entry.ExpiresAt) {
		return entry.Ips, nil
	}

	ips, err := d.Resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	d.Mutex.Lock()
	d.Cache[host] = dnsEntry{
		Ips:       ips,
		ExpiresAt: now.Add(d.TTL),
	}
	d.Mutex.Unlock()

	return ips, nil
}

func (d *DNSCache) LookupHost(ctx context.Context, host string) (string, error) {
	ips, err := d.LookupIPAddr(ctx, host)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", &net.DNSError{Err: "no addresses found", Name: host}
	}
	return ips[0].IP.String(), nil
}

func (d *DNSCache) Clear() {
	d.Mutex.Lock()
	d.Cache = make(map[string]dnsEntry)
	d.Mutex.Unlock()
}

func (d *DNSCache) Size() int {
	d.Mutex.RLock()
	defer d.Mutex.RUnlock()
	return len(d.Cache)
}

func (d *DNSCache) Prune() int {
	now := time.Now()
	pruned := 0

	d.Mutex.Lock()
	for host, entry := range d.Cache {
		if now.After(entry.ExpiresAt) {
			delete(d.Cache, host)
			pruned++
		}
	}
	d.Mutex.Unlock()

	return pruned
}
