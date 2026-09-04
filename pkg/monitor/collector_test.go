package monitor

import (
	"testing"
)

func TestCollector(t *testing.T) {
	c := NewCollector()
	info, err := c.GetBasicInfo()
	if err != nil {
		t.Fatalf("GetBasicInfo failed: %v", err)
	}
	t.Logf("BasicInfo: %+v", info)

	report, err := c.GetReport()
	if err != nil {
		t.Fatalf("GetReport failed: %v", err)
	}
	t.Logf("Report: %+v", report)
}
