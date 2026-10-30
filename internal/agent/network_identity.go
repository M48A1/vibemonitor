//go:build linux && (amd64 || arm64)

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"vibemonitor/pkg/protocol"
)

const networkIdentityInterval = 7 * 24 * time.Hour

// Read YouTube Premium's explicit contentRegion, as used by xykt/IPQuality.
// This is a YouTube service region, not a general Google or IP-geolocation result.
// Missing/malformed data stays unknown; never default it to US or a language code.
var youtubeContentRegion = regexp.MustCompile(`"contentRegion"\s*:\s*"([A-Z]{2})"`)

func parseYouTubeRegion(body []byte) string {
	matches := youtubeContentRegion.FindAllSubmatch(body, -1)
	region := ""
	for _, match := range matches {
		code := string(match[1])
		if region != "" && region != code {
			return ""
		}
		region = code
	}
	return region
}

func identityGET(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	// Consent preference only: no copied visitor/session identifiers or forced region.
	if req.URL.Hostname() == "www.youtube.com" && req.URL.Path == "/premium" {
		req.AddCookie(&http.Cookie{Name: "CONSENT", Value: "YES+cb.20220301-11-p0.en+FX+700"})
	}
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

func lookupYouTubeRegion(ctx context.Context, client *http.Client) (string, error) {
	body, err := identityGET(ctx, client, "https://www.youtube.com/premium?hl=en", 2<<20)
	if err != nil {
		return "", err
	}
	region := parseYouTubeRegion(body)
	if region == "" {
		return "", fmt.Errorf("YouTube contentRegion unavailable")
	}
	return region, nil
}

// Each client forces one address family, including redirects; IPv6 must never
// silently fall back to IPv4 and report the IPv4 region as an IPv6 result.
func newIdentityHTTPClient(network string) *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		TLSHandshakeTimeout: 5 * time.Second,
		IdleConnTimeout:     30 * time.Second,
	}
	return &http.Client{Timeout: 15 * time.Second, Transport: transport}
}

func collectNetworkIdentity(ctx context.Context, ipv4, ipv6 *http.Client) protocol.NetworkIdentity {
	identity := protocol.NetworkIdentity{}
	var err error
	identity.ASN, identity.ASName, err = lookupASN(ctx, ipv4)
	if err != nil && ctx.Err() == nil {
		log.Printf("[Agent] ASN lookup unavailable: %v", err)
	}
	identity.YouTubeRegion, err = lookupYouTubeRegion(ctx, ipv4)
	if err != nil && ctx.Err() == nil {
		log.Printf("[Agent] YouTube IPv4 region unavailable: %v", err)
	}
	identity.YouTubeRegionIPv6, err = lookupYouTubeRegion(ctx, ipv6)
	if err != nil && ctx.Err() == nil {
		log.Printf("[Agent] YouTube IPv6 region unavailable: %v", err)
	}
	identity.CheckedAt = time.Now().UTC()
	return identity
}

func (c *Client) runNetworkIdentityWorker(ctx context.Context, refreshed chan<- struct{}) {
	// No account cookies, proxy environment, or forced country parameter.
	ipv4 := newIdentityHTTPClient("tcp4")
	ipv6 := newIdentityHTTPClient("tcp6")
	defer ipv4.CloseIdleConnections()
	defer ipv6.CloseIdleConnections()
	ticker := time.NewTicker(networkIdentityInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		identity := collectNetworkIdentity(ctx, ipv4, ipv6)
		if ctx.Err() != nil {
			return
		}
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
