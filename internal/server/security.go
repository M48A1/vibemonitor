package server

import (
	"errors"
	"net"
	"net/http"
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

func requestHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}

func clearAdminCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "admin_token", Path: "/", MaxAge: -1, HttpOnly: true, Secure: requestHTTPS(r), SameSite: http.SameSiteStrictMode})
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
