package store

import (
	"context"
	"testing"
	"time"
	"vibemonitor/pkg/protocol"
)

func TestSettingsWritesAllowLiveReports(t *testing.T) {
	for _, kind := range []string{"create", "edit", "edit-traffic", "settings", "password", "icon", "theme"} {
		t.Run(kind, func(t *testing.T) {
			s := deletionStore(t)
			mutate := func() error {
				switch kind {
				case "create":
					_, err := s.CreateNode("new", "", "")
					return err
				case "edit":
					return s.UpdateNode("survivor", "renamed", "", "", 1)
				case "edit-traffic":
					used := 1.0
					return s.UpdateNodeWithOptions("survivor", NodeOptions{ResetDay: -1, InitialUsedGB: -1, TrafficLimitGB: -1, CycleUsedGB: &used})
				case "settings":
					return s.UpdateSettings("new title", nil, "new-password")
				case "password":
					return s.SetAdminPassword("new-password")
				case "icon":
					_, err := s.SaveSiteIcon(nil, "")
					return err
				default:
					return s.SelectTheme("serverstatus")
				}
			}
			conn, err := s.sdb.db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			before := s.sdb.db.Stats().WaitCount
			done := make(chan error, 1)
			go func() { done <- mutate() }()
			deadline := time.Now().Add(3 * time.Second)
			for s.sdb.db.Stats().WaitCount == before {
				if time.Now().After(deadline) {
					t.Fatal("write not reached")
				}
				time.Sleep(time.Millisecond)
			}
			live := make(chan error, 1)
			go func() {
				s.GetDashboardNodes()
				if kind == "edit-traffic" {
					s.mu.Lock()
					s.nodes["survivor"].CurrentCycleUsed += 42
					s.mu.Unlock()
				}
				_, err := s.IngestBasicInfo("survivor-token", protocol.BasicInfo{OS: "arrived-during-write"})
				live <- err
			}()
			select {
			case err := <-live:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("live store blocked by disk")
			}
			if kind == "edit" && s.GetNode("survivor").Name != "survivor" {
				t.Fatal("uncommitted edit visible")
			}
			conn.Close()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if kind == "edit-traffic" && s.GetNode("survivor").CurrentCycleUsed != 42 {
				t.Fatal("traffic accrued during correction was lost")
			}
			if s.GetNode("survivor").BasicInfo.OS != "arrived-during-write" {
				t.Fatal("concurrent report overwritten")
			}
			if err := s.Save(); err != nil {
				t.Fatal(err)
			}
			disk, err := s.sdb.loadNodes()
			if err != nil {
				t.Fatal(err)
			}
			if disk["survivor"].BasicInfo.OS != "arrived-during-write" {
				t.Fatal("concurrent report not persisted")
			}
		})
	}
}

func TestSettingsTransactionFailureDoesNotPublish(t *testing.T) {
	s := deletionStore(t)
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER fail_config BEFORE UPDATE ON config BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	previous := s.GetConfig()
	if err := s.UpdateSettings("should not appear", nil, ""); err == nil {
		t.Fatal("expected failure")
	}
	if s.GetConfig().SiteTitle != previous.SiteTitle {
		t.Fatal("uncommitted config visible")
	}
	if err := s.UpdateNode("survivor", "should not appear", "", "", 0); err == nil {
		t.Fatal("expected failure")
	}
	if s.GetNode("survivor").Name != "survivor" {
		t.Fatal("uncommitted node visible")
	}
	if _, err := s.CreateNode("should not appear", "", ""); err == nil {
		t.Fatal("expected failure")
	}
	if len(s.GetNodes()) != 2 {
		t.Fatal("uncommitted node added")
	}
	if _, err := s.sdb.db.Exec("DROP TRIGGER fail_config"); err != nil {
		t.Fatal(err)
	}
}
