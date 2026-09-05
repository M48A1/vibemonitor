package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"vibemonitor/internal/store"
)

func TestNodeCredentialIsolation(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "data.json")
	s, err := New(Options{DataFile: dataPath, AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.store.Close() })
	n, err := s.store.CreateNode("test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	uuid, token := n.UUID, n.Token
	h := s.Handler()
	request := func(method, path, body, credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if credential != "" {
			r.Header.Set("Authorization", "Bearer "+credential)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	assertPublic := func(data []byte) {
		t.Helper()
		if strings.Contains(string(data), token) || strings.Contains(string(data), `"token"`) {
			t.Fatalf("public response leaks credentials: %s", data)
		}
		if !strings.Contains(string(data), uuid) {
			t.Fatal("public response lost node")
		}
	}
	assertPublic(request("GET", "/api/nodes", "", "").Body.Bytes())
	for _, credential := range []string{"", uuid, "invalid"} {
		w := request("GET", "/api/admin/nodes/"+uuid+"/token", "", credential)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("credential endpoint status: %d", w.Code)
		}
	}
	w := request("POST", "/api/admin/login", `{"password":"test-password"}`, "")
	var login struct{ Token string }
	json.Unmarshal(w.Body.Bytes(), &login)
	w = request("GET", "/api/admin/nodes/"+uuid+"/token", "", login.Token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), token) {
		t.Fatal("administrator cannot retrieve token")
	}
	for _, method := range []string{"agent.basicInfo", "agent.report"} {
		for _, credential := range []string{uuid, "invalid", token} {
			body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":{}}`
			w := request("POST", "/api/clients/v2/rpc", body, credential)
			want := http.StatusUnauthorized
			if credential == token {
				want = http.StatusOK
			}
			if w.Code != want {
				t.Fatalf("%s credential %q: got %d want %d", method, credential, w.Code, want)
			}
		}
	}
	// Both initial WebSocket messages and broadcasts must use public data.
	ts := httptest.NewServer(h)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/clients", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for i := 0; i < 2; i++ {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		assertPublic(data)
		if i == 0 {
			s.wsHub.broadcastNodes()
		}
	}
	// Removing credentials from public copies must not erase persisted secrets.
	if err := s.store.Save(); err != nil {
		t.Fatal(err)
	}
	disk, err := os.ReadFile(dataPath)
	if err != nil || !strings.Contains(string(disk), token) {
		t.Fatal("persisted node token lost", err)
	}
}

func TestInstallerValidation(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "data.json"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n, err := st.CreateNode("test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	h := HandleInstallScript(st)
	for _, token := range []string{"", "$(printf INJECTED)", "bad", n.UUID} {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest("GET", "/install.sh?token="+url.QueryEscape(token), nil))
		if w.Code < 400 {
			t.Fatalf("accepted invalid token %q", token)
		}
	}
	for _, host := range []string{"evil$(id).example", "evil\".example", "evil`id`.example"} {
		r := httptest.NewRequest("GET", "/install.sh?token="+n.Token, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h(w, r)
		if w.Code != 400 {
			t.Fatalf("accepted host %q", host)
		}
	}
	r := httptest.NewRequest("GET", "https://monitor.example:1314/install.sh?token="+n.Token, nil)
	w := httptest.NewRecorder()
	h(w, r)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	script := w.Body.String()
	// Execute only architecture detection, before any installation or download.
	standalone, err := os.ReadFile("../../install.sh")
	if err != nil {
		t.Fatal(err)
	}
	standaloneText := string(standalone)
	start := strings.Index(standaloneText, "detect_arch() {")
	end := strings.Index(standaloneText, "check_dependencies() {")
	if start < 0 || end < start {
		t.Fatal("missing standalone architecture detection")
	}
	for name, detection := range map[string]string{
		"dynamic":    strings.Split(script, "# Installation begins here")[0],
		"standalone": "info() { :; }; error() { echo \"$1\"; exit 1; };\n" + standaloneText[start:end] + "\ndetect_arch\n",
	} {
		for _, platform := range []string{"Linux/x86_64", "Linux/amd64", "Linux/aarch64", "Linux/armv7l", "Linux/i686", "Darwin/x86_64", "FreeBSD/x86_64", "MINGW64_NT/x86_64"} {
			machineOS, arch, _ := strings.Cut(platform, "/")
			t.Run(name+"/"+platform, func(t *testing.T) {
				cmd := exec.Command("bash", "-c", "uname() { if [ \"$1\" = \"-s\" ]; then echo \"$TEST_MACHINE_OS\"; else echo \"$TEST_MACHINE_ARCH\"; fi; }; id() { echo 0; };\n"+detection)
				cmd.Env = append(os.Environ(), "TEST_MACHINE_ARCH="+arch, "TEST_MACHINE_OS="+machineOS)
				out, err := cmd.CombinedOutput()
				supported := machineOS == "Linux" && (arch == "x86_64" || arch == "amd64")
				if supported && err != nil {
					t.Fatalf("supported machine rejected: %s %v", out, err)
				}
				if !supported && (err == nil || !strings.Contains(string(out), "is supported")) {
					t.Fatalf("unsupported machine not rejected: %s %v", out, err)
				}
			})
		}
	}
	cmd := exec.Command("bash", "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("installer syntax: %s %v", out, err)
	}
}
