package store

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func BenchmarkEditNodeWithHistory(b *testing.B) {
	s, err := New(filepath.Join(b.TempDir(), "bench.db"), "password")
	if err != nil {
		b.Fatal(err)
	}
	close(s.stopFlush)
	<-s.flushDone
	defer s.Close()
	now := time.Now().Unix()
	for i := 0; i < 30; i++ {
		id := fmt.Sprint(i)
		n := &Node{UUID: id, Token: id, Name: id, Profile: &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "example.com:443"}}}, PingHistory: map[string][]PingSample{}}
		for j := 0; j < 1440; j++ {
			n.PingHistory["route"] = append(n.PingHistory["route"], PingSample{Host: "example.com:443", Method: "tcp", Timestamp: now - int64(j)*60, Latency: 10})
		}
		s.nodes[id] = n
	}
	if err := s.saveLocked(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := s.UpdateNode("0", fmt.Sprintf("edited-%d", i), "", "", 0); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

func BenchmarkDeleteNodeWithHistory(b *testing.B) {
	db, err := openSQLite(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		for _, query := range []string{
			`INSERT INTO nodes(uuid,name,token,data_json) VALUES('deleted','deleted','token','{}')`,
			`UPDATE history_rollup_state SET completed_before=0`,
			`WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq WHERE x<20000) INSERT INTO ping_history(node_uuid,target_name,host,method,timestamp,latency) SELECT 'deleted','route','example.com:443','tcp',x*60,10 FROM seq`,
			`WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq WHERE x<20000) INSERT INTO resource_history(node_uuid,timestamp,cpu_usage) SELECT 'deleted',x*60,10 FROM seq`,
			`UPDATE history_rollup_state SET completed_before=2000000`,
		} {
			if _, err := db.db.Exec(query); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
		if err := db.deleteNode("deleted"); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}
