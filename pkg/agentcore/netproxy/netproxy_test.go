package netproxy

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func mustSelector(t *testing.T, proxyURL string, sites ...string) *Selector {
	t.Helper()
	sel, err := New(proxyURL, sites)
	assert.Require(t, assert.NoErr(t, err))
	return sel
}

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	assert.Require(t, assert.NoErr(t, err))
	return u
}

// TestSelectorParseErrors covers the validation the registry and the runtime
// share: a bad URL or allowlist entry must fail instead of silently connecting
// directly.
func TestSelectorParseErrors(t *testing.T) {
	t.Run("proxy url", func(t *testing.T) {
		for _, bad := range []string{"", "ftp://p:1", "127.0.0.1:7890", "http://", "http://p.example/proxy"} {
			_, err := New(bad, []string{"api.example.com"})
			assert.Err(t, err)
		}
	})
	t.Run("sites", func(t *testing.T) {
		for _, bad := range []string{"", "http://api.example.com", "api.example.com/path", "api.example.com:abc", "api.example.com:70000", ".", "a b.com"} {
			_, err := New("http://127.0.0.1:10801", []string{bad})
			assert.ErrMsgContains(t, err, "network.proxy.sites")
		}
	})
	t.Run("socks5 and https urls are accepted", func(t *testing.T) {
		for _, ok := range []string{"http://127.0.0.1:10801", "https://proxy.example:8443", "socks5://127.0.0.1:1080"} {
			_, err := New(ok, nil)
			assert.NoErr(t, err)
		}
	})
}

func TestSelectorMatch(t *testing.T) {
	sel := mustSelector(t, "http://127.0.0.1:10801",
		"api.openai.com", ".anthropic.com", "serpapi.com:8443")

	t.Run("exact host matches", func(t *testing.T) {
		for _, raw := range []string{"https://api.openai.com/v1/chat", "https://API.OPENAI.COM/v1/chat", "http://api.openai.com:80/x"} {
			assert.True(t, sel.Match(parseURL(t, raw)), raw)
		}
	})
	t.Run("suffix entry covers the domain and its subdomains", func(t *testing.T) {
		for _, raw := range []string{"https://anthropic.com/v1", "https://api.anthropic.com/v1", "https://a.b.anthropic.com/"} {
			assert.True(t, sel.Match(parseURL(t, raw)), raw)
		}
	})
	t.Run("other hosts stay direct", func(t *testing.T) {
		for _, raw := range []string{"https://openai.com/", "https://notanthropic.com/", "http://127.0.0.1:10801/", "https://only.example/"} {
			assert.False(t, sel.Match(parseURL(t, raw)), raw)
		}
	})
	t.Run("port entry only matches that port", func(t *testing.T) {
		assert.True(t, sel.Match(parseURL(t, "https://serpapi.com:8443/search")))
		assert.False(t, sel.Match(parseURL(t, "https://serpapi.com/search")))
	})
	t.Run("star opts everything in", func(t *testing.T) {
		all := mustSelector(t, "http://127.0.0.1:10801", "*")
		for _, raw := range []string{"https://internal.example/x", "http://10.0.0.1:8080/", "https://api.openai.com/"} {
			assert.True(t, all.Match(parseURL(t, raw)), raw)
		}
	})
	t.Run("empty sites never proxied", func(t *testing.T) {
		empty := mustSelector(t, "http://127.0.0.1:10801")
		for _, raw := range []string{"https://api.openai.com/", "http://127.0.0.1:10801/", "https://x.example/"} {
			assert.False(t, empty.Match(parseURL(t, raw)), raw)
		}
	})
	t.Run("nil selector and nil url", func(t *testing.T) {
		var nilSel *Selector
		assert.False(t, nilSel.Match(parseURL(t, "https://api.openai.com/")))
		assert.False(t, sel.Match(nil))
		assert.True(t, nilSel.ProxyFunc() == nil)
	})
}

// TestProxyFuncRoutesRequests proves the http.Transport.Proxy callback sends a
// matched request to the proxy and leaves an unmatched one to the target.
func TestProxyFuncRoutesRequests(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		_, _ = w.Write([]byte("via proxy: " + r.URL.String())) // a forward proxy sees the absolute URL
	}))
	t.Cleanup(proxy.Close)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct"))
	}))
	t.Cleanup(target.Close)

	targetHost := parseURL(t, target.URL).Hostname()
	sel := mustSelector(t, proxy.URL, targetHost+":"+parseURL(t, target.URL).Port())
	client := &http.Client{Transport: &http.Transport{Proxy: sel.ProxyFunc()}}

	body, err := get(client, target.URL+"/doc")
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, body, "via proxy: "+target.URL+"/doc")
	assert.Eq(t, int32(1), proxied.Load())

	// A host outside the allowlist keeps connecting directly.
	direct := mustSelector(t, proxy.URL, "api.openai.com")
	client = &http.Client{Transport: &http.Transport{Proxy: direct.ProxyFunc()}}
	body, err = get(client, target.URL+"/doc")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "direct", body)
	assert.Eq(t, int32(1), proxied.Load())
}

// TestProxyFuncSocks5 proves a socks5:// proxy URL works through the standard
// transport (Go 1.24+ dials it natively, so no extra dependency is needed).
func TestProxyFuncSocks5(t *testing.T) {
	var connects atomic.Int32
	proxyAddr, err := listenSocks5(&connects)
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = proxyAddr.Close() })

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("through socks5"))
	}))
	t.Cleanup(target.Close)

	targetURL := parseURL(t, target.URL)
	sel := mustSelector(t, "socks5://"+proxyAddr.Addr().String(), targetURL.Hostname())
	client := &http.Client{Transport: &http.Transport{Proxy: sel.ProxyFunc()}}
	body, err := get(client, target.URL+"/doc")
	assert.Require(t, assert.NoErr(t, err))
	assert.Eq(t, "through socks5", body)
	assert.Eq(t, int32(1), connects.Load())
}

func get(client *http.Client, raw string) (string, error) {
	resp, err := client.Get(raw)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

// listenSocks5 starts a minimal SOCKS5 CONNECT server (no authentication) and
// counts the connections it accepted.
func listenSocks5(connects *atomic.Int32) (net.Listener, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connects.Add(1)
			go serveSocks5(conn)
		}
	}()
	return ln, nil
}

func serveSocks5(conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	head := make([]byte, 2)
	if _, err := io.ReadFull(br, head); err != nil || head[0] != 5 {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil { // no authentication
		return
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[1] != 1 { // CONNECT only
		return
	}
	host, err := socks5Host(br, req[3])
	if err != nil {
		return
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(br, portBytes); err != nil {
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes)))))
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	go func() { _, _ = io.Copy(target, br) }()
	_, _ = io.Copy(conn, target)
}

func socks5Host(br *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 1:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(br, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(br, length); err != nil {
			return "", err
		}
		buf := make([]byte, int(length[0]))
		if _, err := io.ReadFull(br, buf); err != nil {
			return "", err
		}
		return string(buf), nil
	case 4:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(br, buf); err != nil {
			return "", err
		}
		return net.IP(buf).String(), nil
	}
	return "", io.ErrUnexpectedEOF
}
