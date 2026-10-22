package agent

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type endlessBody struct {
	read   int
	closed bool
}

func (b *endlessBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}
func (b *endlessBody) Close() error { b.closed = true; return nil }

func TestRPCResponseBoundsUntrustedBodies(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusOK} {
		body := &endlessBody{}
		_, err := readRPCResponse(&http.Response{StatusCode: status, Body: body})
		limit := maxRPCResponseBytes
		if status != http.StatusOK {
			limit = maxRPCErrorBytes
		}
		if err == nil || body.read != limit+1 || !body.closed {
			t.Fatalf("status %d: err %v, bytes %d, closed %v", status, err, body.read, body.closed)
		}
		if status != http.StatusOK && (!strings.Contains(err.Error(), "truncated") || len(err.Error()) > maxRPCErrorBytes+100) {
			t.Fatalf("unbounded error log: %d bytes", len(err.Error()))
		}
	}
}

func TestRPCResponseKeepsNormalResults(t *testing.T) {
	resp, err := readRPCResponse(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","result":{"ping_targets":[]}}`))})
	if err != nil || resp == nil || resp.Result == nil {
		t.Fatalf("normal response = %+v, %v", resp, err)
	}
	_, err = readRPCResponse(&http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","error":{"code":-1,"message":"denied"}}`))})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatal("RPC error lost")
	}
}

func TestRepeatedErrorsRemainVisibleWithoutPerSecondSpam(t *testing.T) {
	var limiter repeatedErrorLog
	now := time.Now()
	err := errors.New("server unavailable")
	if _, emit := limiter.message(err, now); !emit {
		t.Fatal("first failure hidden")
	}
	for second := 1; second < 30; second++ {
		if _, emit := limiter.message(err, now.Add(time.Duration(second)*time.Second)); emit {
			t.Fatal("identical failure logged every second")
		}
	}
	if message, emit := limiter.message(err, now.Add(30*time.Second)); !emit || !strings.Contains(message, "29 repeated failures") {
		t.Fatalf("missing failure summary: %q, %v", message, emit)
	}
	if _, emit := limiter.message(errors.New("token rejected"), now.Add(31*time.Second)); !emit {
		t.Fatal("changed failure hidden")
	}
	limiter.reset()
	if _, emit := limiter.message(err, now.Add(32*time.Second)); !emit {
		t.Fatal("failure after recovery hidden")
	}
}
