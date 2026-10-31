package server

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

// adminPagePaths is the management area served by admin_pages.go.
var adminPagePaths = map[string]string{
	"/admin/nodes":    "节点管理",
	"/admin/settings": "基本设置",
	"/admin/theme":    "主题管理",
	"/admin/telegram": "外部通信设置",
}

// redirectClient keeps 302 responses visible for the assertions below and keeps
// cookies, so a test can sign in first and still inspect the next redirect.
func redirectClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// TestAdminPagesAreServed keeps the management area reachable at stable URLs for
// a signed-in admin.
func TestAdminPagesAreServed(t *testing.T) {
	_, ts, client := newTestServer(t)
	login(t, client, ts.URL)
	for path, want := range adminPagePaths {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			t.Fatalf("GET %s body: %v", path, readErr)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want %d", path, res.StatusCode, http.StatusOK)
			continue
		}
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s Content-Type = %q, want text/html", path, ct)
		}
		if !strings.Contains(string(body), want) {
			t.Errorf("GET %s does not mention %q", path, want)
		}
		if strings.Contains(string(body), `id="nodeGrid"`) {
			t.Errorf("GET %s served the dashboard shell instead of a management page", path)
		}
	}
}

// TestAdminRootRedirects sends a signed-in visitor to the node list, while an
// anonymous one goes back to the dashboard.
func TestAdminRootRedirects(t *testing.T) {
	_, ts, _ := newTestServer(t)
	client := redirectClient(t)
	for _, path := range []string{"/admin", "/admin/"} {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusFound {
			t.Errorf("GET %s status = %d, want %d", path, res.StatusCode, http.StatusFound)
		}
		if got := res.Header.Get("Location"); got != "/" {
			t.Errorf("anonymous GET %s Location = %q, want /", path, got)
		}
	}
	login(t, client, ts.URL)
	for _, path := range []string{"/admin", "/admin/"} {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s after login: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusFound {
			t.Errorf("GET %s status = %d, want %d", path, res.StatusCode, http.StatusFound)
		}
		if got := res.Header.Get("Location"); got != "/admin/nodes" {
			t.Errorf("GET %s Location = %q, want /admin/nodes", path, got)
		}
	}
}

// TestAdminPagesRequireLogin keeps anonymous callers away from the management
// shell entirely: no forms, ids or navigation, just a bounce to the dashboard
// where the login dialog lives.
func TestAdminPagesRequireLogin(t *testing.T) {
	_, ts, _ := newTestServer(t)
	client := redirectClient(t)
	for path := range adminPagePaths {
		res, err := client.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			t.Fatalf("GET %s body: %v", path, readErr)
		}
		if res.StatusCode != http.StatusFound {
			t.Errorf("GET %s status = %d, want %d", path, res.StatusCode, http.StatusFound)
		}
		if got := res.Header.Get("Location"); got != "/" {
			t.Errorf("GET %s Location = %q, want /", path, got)
		}
		if got := res.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("GET %s Cache-Control = %q, want no-store", path, got)
		}
		if strings.Contains(string(body), `id="adminContent"`) {
			t.Errorf("GET %s leaked the management shell to an anonymous caller", path)
		}
	}
}

// TestUnknownAdminPathIsNotFound keeps a typo from serving the dashboard shell,
// which would look like a broken management page.
func TestUnknownAdminPathIsNotFound(t *testing.T) {
	_, ts, _ := newTestServer(t)
	client := &http.Client{}
	res, err := client.Get(ts.URL + "/admin/nodse")
	if err != nil {
		t.Fatalf("GET unknown admin path: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

// TestAdminPagesRequireLoginOnTheAPIs proves the pages themselves carry no
// secrets: the data behind them stays behind the admin session.
func TestAdminPagesRequireLoginOnTheAPIs(t *testing.T) {
	_, ts, _ := newTestServer(t)
	client := &http.Client{}
	res, err := client.Get(ts.URL + "/api/admin/themes")
	if err != nil {
		t.Fatalf("GET themes: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /api/admin/themes status = %d, want %d", res.StatusCode, http.StatusUnauthorized)
	}
}
