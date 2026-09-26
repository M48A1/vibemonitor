package server

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminCanSetCurrentCycleUsage(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.store.CreateNode("manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	s.adminTokens.Store("admin", time.Now().Add(time.Hour))
	h := s.Handler()
	put := func(body, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPut, "/api/admin/nodes/"+n.UUID, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := put(`{"name":"manual","cycle_used_gb":2.5}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated correction status: %d", w.Code)
	}
	if w := put(`{"name":"manual","cycle_used_gb":2.5}`, "admin"); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	const gib = int64(1024 * 1024 * 1024)
	if got := s.store.GetNode(n.UUID).CycleTotalUsed; got != 5*gib/2 {
		t.Fatalf("corrected usage: got %d, want %d", got, 5*gib/2)
	}
	if w := put(`{"name":"renamed"}`, "admin"); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if got := s.store.GetNode(n.UUID).CycleTotalUsed; got != 5*gib/2 {
		t.Fatalf("ordinary edit overwrote usage: %d", got)
	}
	if w := put(`{"name":"renamed","cycle_used_gb":-1}`, "admin"); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid correction status: %d", w.Code)
	}
	if w := put(`{"name":"renamed","cycle_used_gb":0}`, "admin"); w.Code != http.StatusOK || s.store.GetNode(n.UUID).CycleTotalUsed != 0 {
		t.Fatalf("zero correction failed: %d %s", w.Code, w.Body.String())
	}
}
