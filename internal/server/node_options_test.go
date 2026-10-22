package server

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"vibemonitor/internal/store"
)

func TestNodeTrafficValidationAndOmittedQuota(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.adminTokens.Store("test-session", time.Now().Add(time.Hour))
	n, err := s.store.CreateNodeWithOptions(store.NodeOptions{Name: "original", TrafficLimitGB: 0.04, InitialUsedGB: 2, ResetDay: 15})
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	request := func(method, path, body string) int {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-session")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	for _, method := range []string{"POST", "PUT"} {
		path := "/api/admin/nodes"
		if method == "PUT" {
			path += "/" + n.UUID
		}
		for _, body := range []string{`{"traffic_limit_gb":-1}`, `{"traffic_limit_gb":8589934592}`, `{"initial_used_gb":-1}`, `{"initial_used_gb":1e20}`, `{"reset_day":-1}`, `{"reset_day":32}`} {
			if status := request(method, path, body); status != 400 {
				t.Errorf("%s %s: got %d want 400", method, body, status)
			}
		}
	}
	if len(s.store.GetNodes()) != 1 {
		t.Fatal("invalid create changed store")
	}
	if status := request("PUT", "/api/admin/nodes/"+n.UUID, `{"name":"renamed"}`); status != 200 {
		t.Fatalf("edit: %d", status)
	}
	got := s.store.GetNode(n.UUID)
	if got.Name != "renamed" || got.TrafficLimit != n.TrafficLimit || got.InitialUsed != n.InitialUsed || got.ResetDay != 15 {
		t.Fatalf("omitted traffic fields overwritten: %+v", got)
	}
	if status := request("PUT", "/api/admin/nodes/"+n.UUID, `{"traffic_limit_gb":0}`); status != 200 {
		t.Fatalf("clear quota: %d", status)
	}
	if s.store.GetNode(n.UUID).TrafficLimit != 0 {
		t.Fatal("explicit zero must clear quota")
	}
}
