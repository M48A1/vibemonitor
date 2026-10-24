package server

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"vibemonitor/internal/store"
)

func TestRankingsEndpoint(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, query := range []string{"", "?period=day", "?period=week", "?period=month", "?period=invalid"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/rankings"+query, nil))
		if strings.Contains(query, "invalid") {
			if w.Code != 400 {
				t.Fatal(w.Code)
			}
			continue
		}
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
		var response store.RankingsResponse
		if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if len(response.Groups) != 5 || response.Timezone != "UTC+8" {
			t.Fatalf("bad response: %+v", response)
		}
		for _, group := range response.Groups {
			if group.Entries == nil || len(group.Entries) != 0 {
				t.Fatalf("empty data: %+v", group)
			}
		}
	}
}
