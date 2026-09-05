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
		return strings.TrimPrefix(auth, "Bearer ")
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
	return net.ParseIP(host).IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
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

func (l *loginLimiter) allow(r *http.Request) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.clients == nil {
		l.clients = make(map[string]loginWindow)
	}
	for key, value := range l.clients {
		if !now.Before(value.expires) {
			delete(l.clients, key)
		}
	}
	// Do not trust client-supplied forwarding headers for rate limiting.
	key, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		key = r.RemoteAddr
	}
	v, ok := l.clients[key]
	if !ok {
		if len(l.clients) >= 4096 {
			return false
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
