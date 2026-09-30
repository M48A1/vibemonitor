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

func (l *loginLimiter) allow(r *http.Request) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.clients == nil {
		l.clients = make(map[string]loginWindow)
	}
	// Do not trust client-supplied forwarding headers for rate limiting.
	key, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		key = r.RemoteAddr
	}
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
