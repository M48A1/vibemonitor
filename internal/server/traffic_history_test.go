package server

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"vibemonitor/internal/store"
)

func TestTrafficHistoryEndpoint(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := s.store.CreateNodeWithOptions(store.NodeOptions{Name: "traffic", ResetDay: 15})
	if err != nil {
		t.Fatal(err)
	}
	handler := s.Handler()
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"uuid=" + node.UUID + "&range=current", 200},
		{"uuid=" + node.UUID + "&range=previous", 200},
		{"uuid=" + node.UUID + "&range=three", 200},
		{"uuid=" + node.UUID + "&range=1h", 400},
		{"range=current", 400},
		{"uuid=missing&range=current", 404},
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/nodes/traffic-history?"+tc.query, nil))
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body.String())
		}
		if w.Code == 200 {
			var response store.TrafficHistoryResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.ResetDay != 15 || len(response.Days) < 28 || response.Total != 0 {
				t.Fatalf("bad response: %+v", response)
			}
			for _, day := range response.Days {
				if day.Bytes != 0 {
					t.Fatal("unobserved day must have zero traffic")
				}
			}
		}
	}
}
