package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const maxRequestBytes = 1 << 20

func adminToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return token
		}
		return ""
	}
	if cookie, err := r.Cookie("admin_token"); err == nil {
		return cookie.Value
	}
	return ""
}

func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	var proxies []netip.Prefix
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		prefix, err := netip.ParsePrefix(entry)
		if err != nil {
			addr, addrErr := netip.ParseAddr(entry)
			if addrErr != nil {
				return nil, fmt.Errorf("invalid trusted proxy %q: expected IP address or CIDR", entry)
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		proxies = append(proxies, prefix.Masked())
	}
	return proxies, nil
}

func trustedProxyAddress(ip netip.Addr, trustedProxies []netip.Prefix) bool {
	ip = ip.Unmap()
	if ip.IsLoopback() {
		return true
	}
	for _, proxy := range trustedProxies {
		if proxy.Contains(ip) {
			return true
		}
	}
	return false
}

// Only a trusted immediate peer may supply a forwarding chain. Walking it from
// the right stops at the first untrusted hop, ignoring any prefix it supplied.
func requestClientIP(r *http.Request, trustedProxies []netip.Prefix) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	key := peer.String()
	if !trustedProxyAddress(peer, trustedProxies) {
		return key
	}
	forwarded := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if forwarded == "" || len(forwarded) > 4096 {
		return key
	}
	hops := strings.Split(forwarded, ",")
	if len(hops) > 32 {
		return key
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil || ip.Zone() != "" {
			return key
		}
		client = ip.Unmap()
		if !trustedProxyAddress(client, trustedProxies) {
			return client.String()
		}
	}
	return client.String()
}

func requestHTTPS(r *http.Request, trustedProxies ...netip.Prefix) bool {
	if r.TLS != nil {
		return true
	}
	if r.Header.Get("X-Forwarded-Proto") != "https" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return trustedProxyAddress(ip, trustedProxies)
}

func clearAdminCookie(w http.ResponseWriter, r *http.Request, trustedProxies ...netip.Prefix) {
	http.SetCookie(w, &http.Cookie{Name: "admin_token", Path: "/", MaxAge: -1, HttpOnly: true, Secure: requestHTTPS(r, trustedProxies...), SameSite: http.SameSiteStrictMode})
}

func jsonErrorStatus(err error) int {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

type loginWindow struct {
	count   int
	expires time.Time
}
type loginLimiter struct {
	mu      sync.Mutex
	clients map[string]loginWindow
}

const maxLoginClients = 4096

func (l *loginLimiter) pruneExpired() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpiredLocked(time.Now())
}

func (l *loginLimiter) pruneExpiredLocked(now time.Time) {
	for key, value := range l.clients {
		if !now.Before(value.expires) {
			delete(l.clients, key)
		}
	}
}

func (l *loginLimiter) allow(r *http.Request, trustedProxies ...netip.Prefix) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.clients == nil {
		l.clients = make(map[string]loginWindow)
	}
	key := requestClientIP(r, trustedProxies)
	v, ok := l.clients[key]
	if ok && !now.Before(v.expires) {
		delete(l.clients, key)
		ok = false
	}
	if !ok {
		if len(l.clients) >= maxLoginClients {
			l.pruneExpiredLocked(now)
		}
		if len(l.clients) >= maxLoginClients {
			// Keep the map bounded without locking out every new address.
			var oldestKey string
			var oldestExpiry time.Time
			for candidate, window := range l.clients {
				if oldestKey == "" || window.expires.Before(oldestExpiry) {
					oldestKey, oldestExpiry = candidate, window.expires
				}
			}
			delete(l.clients, oldestKey)
		}
		v.expires = now.Add(5 * time.Minute)
	}
	if v.count >= 10 {
		return false
	}
	v.count++
	l.clients[key] = v
	return true
}
