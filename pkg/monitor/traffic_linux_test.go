//go:build linux && (amd64 || arm64)

package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoTrafficInterfaceSkipsStackedDevices(t *testing.T) {
	root := t.TempDir()
	makeDevice := func(name, kind string, children ...string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "type"), []byte(kind), 0644); err != nil {
			t.Fatal(err)
		}
		for _, child := range children {
			if err := os.MkdirAll(filepath.Join(path, child), 0755); err != nil {
				t.Fatal(err)
			}
		}
	}
	makeDevice("eth0", "1", "device")
	makeDevice("renamedTunnel", "65534")
	makeDevice("guestTap", "1", "brport")
	makeDevice("mac0", "1", "lower_eth0")
	makeDevice("bridge0", "1", "bridge")
	makeDevice("renamedBond", "1")
	makeDevice("renamedOverlay", "1")
	makeDevice("eno1", "1", "device", "master")
	for name, devtype := range map[string]string{"renamedBond": "bond", "renamedOverlay": "vxlan"} {
		if err := os.WriteFile(filepath.Join(root, name, "uevent"), []byte("DEVTYPE="+devtype+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]bool{
		"eth0": true, "eno1": true, "renamedTunnel": false,
		"guestTap": false, "mac0": false, "bridge0": false,
		"renamedBond": false, "renamedOverlay": false,
	} {
		if got := autoTrafficInterface(root, name); got != want {
			t.Errorf("%s selected = %v, want %v", name, got, want)
		}
	}
}

func TestReadKernelBootID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boot_id")
	if err := os.WriteFile(path, []byte("boot-123\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := readKernelBootID(path); got != "boot-123" {
		t.Fatalf("boot ID = %q", got)
	}
}
