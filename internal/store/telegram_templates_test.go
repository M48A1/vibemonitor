package store

import "testing"

func TestRecoveryTemplateLastSeen(t *testing.T) {
	custom := TelegramTemplates{Recovery: "{node} 已上线：{last_seen}"}
	if err := validateTelegramTemplates(custom); err != nil {
		t.Fatal(err)
	}
	if custom.Effective().Recovery != custom.Recovery {
		t.Fatal("custom recovery template changed")
	}
}
