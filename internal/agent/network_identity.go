//go:build linux && (amd64 || arm64)

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"

	"vibemonitor/pkg/protocol"
)

const networkIdentityInterval = 7 * 24 * time.Hour

// Google has no supported IP-region API. Only accept its explicit homepage
// country footer, never infer a country from language, redirects or IP databases.
var googleCountryFooter = regexp.MustCompile(`(?is)<div\b[^>]*\bclass\s*=\s*["'][^"']*\buU7dJb\b[^"']*["'][^>]*>\s*([^<]+)\s*</div>`)

func parseGoogleRegion(body []byte) string {
	match := googleCountryFooter.FindSubmatch(body)
	if len(match) != 2 {
		return ""
	}
	region := strings.Join(strings.Fields(html.UnescapeString(string(match[1]))), " ")
	if len(region) == 0 || len(region) > 100 {
		return ""
	}
	for _, r := range region {
		if !unicode.IsLetter(r) && !strings.ContainsRune(" -'’.(),&", r) {
			return ""
		}
	}
	return region
}

func identityGET(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds size limit")
	}
	return body, nil
}

func lookupASN(ctx context.Context, client *http.Client) (string, string, error) {
	body, err := identityGET(ctx, client, "https://ipwho.is/?lang=en&fields=success,connection.asn,connection.org", 64<<10)
	if err != nil {
		return "", "", err
	}
	var result struct {
		Success    bool `json:"success"`
		Connection struct {
			ASN uint32 `json:"asn"`
			Org string `json:"org"`
		} `json:"connection"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", err
	}
	name := strings.Join(strings.Fields(result.Connection.Org), " ")
	if !result.Success || result.Connection.ASN == 0 || name == "" || len(name) > 256 {
		return "", "", fmt.Errorf("ASN response missing valid network details")
	}
	return fmt.Sprintf("AS%d", result.Connection.ASN), name, nil
}

func lookupGoogleRegion(ctx context.Context, client *http.Client) (string, error) {
	body, err := identityGET(ctx, client, "https://www.google.com/?hl=en", 2<<20)
	if err != nil {
		return "", err
	}
	region := parseGoogleRegion(body)
	if region == "" {
		return "", fmt.Errorf("Google country footer unavailable")
	}
	return region, nil
}

func (c *Client) runNetworkIdentityWorker(ctx context.Context, refreshed chan<- struct{}) {
	// Use the node's direct IPv4 egress, matching the project's IPv4 probes.
	// No account cookies, proxy environment, or forced Google country parameter.
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp4", address)
		},
		TLSHandshakeTimeout: 5 * time.Second,
		IdleConnTimeout:     30 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport}
	ticker := time.NewTicker(networkIdentityInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		identity := protocol.NetworkIdentity{}
		var err error
		identity.ASN, identity.ASName, err = lookupASN(ctx, client)
		if err != nil && ctx.Err() == nil {
			log.Printf("[Agent] ASN lookup unavailable: %v", err)
		}
		identity.GoogleRegion, err = lookupGoogleRegion(ctx, client)
		if err != nil && ctx.Err() == nil {
			log.Printf("[Agent] Google region unavailable: %v", err)
		}
		if ctx.Err() != nil {
			return
		}
		identity.CheckedAt = time.Now().UTC()
		c.mu.Lock()
		c.networkIdentity = &identity
		c.mu.Unlock()
		select {
		case refreshed <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
