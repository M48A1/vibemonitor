package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

func TestHistoryHandlersRespectCanceledRequests(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.store.CreateNodeWithOptions(store.NodeOptions{Name: "node", Profile: &store.NodeProfile{
		Targets: []protocol.PingTarget{{Name: "route", Host: "example.com:443"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.Report{UpdatedAt: time.Now(), PingResults: []protocol.PingResult{{Name: "route", Host: "example.com:443", Method: "tcp", Latency: 12}}}
	r.CPU.Usage = 20
	if _, err = s.store.IngestReport(n.Token, r); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{
		"/api/nodes/ping-history?uuid=" + n.UUID + "&target=route&range=1h",
		"/api/nodes/resource-history?uuid=" + n.UUID + "&metric=cpu&range=1h",
	} {
		t.Run(endpoint, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			request := httptest.NewRequest("GET", endpoint, nil).WithContext(ctx)
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, request)
			if response.Body.Len() != 0 {
				t.Fatalf("canceled request produced a response: %s", response.Body.String())
			}
		})
	}
}

func TestHistoryHandlersReturnNotFoundForDeletedNodes(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.store.CreateNodeWithOptions(store.NodeOptions{Name: "node", Profile: &store.NodeProfile{
		Targets: []protocol.PingTarget{{Name: "route", Host: "example.com:443"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.DeleteNode(n.UUID); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{
		"/api/nodes/ping-history?uuid=" + n.UUID + "&target=route&range=1h",
		"/api/nodes/resource-history?uuid=" + n.UUID + "&metric=cpu&range=1h",
	} {
		t.Run(endpoint, func(t *testing.T) {
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, endpoint, nil))
			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusNotFound || body["error"] != "node not found" {
				t.Fatalf("deleted node history: status=%d body=%s err=%v", response.Code, response.Body.String(), err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			cancelled := httptest.NewRecorder()
			s.Handler().ServeHTTP(cancelled, httptest.NewRequest(http.MethodGet, endpoint, nil).WithContext(ctx))
			if cancelled.Body.Len() != 0 {
				t.Fatal("cancelled missing-node query wrote a response")
			}
		})
	}
}
