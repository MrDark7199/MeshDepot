package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resetTrustedProxies restores the package default so tests stay independent.
func resetTrustedProxies(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { SetTrustedProxies(nil) })
}

func TestDecodeJSONReadsBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ada","count":3}`))
	var target struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if failure := DecodeJSON(request, &target); failure != nil {
		t.Fatalf("decode: %v", failure)
	}
	if target.Name != "ada" || target.Count != 3 {
		t.Fatalf("unexpected target %+v", target)
	}
}

// A DELETE without a body is a normal case and must not become a 422.
func TestDecodeJSONAcceptsEmptyBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/", strings.NewReader(""))
	target := struct {
		Name string `json:"name"`
	}{Name: "unchanged"}

	if failure := DecodeJSON(request, &target); failure != nil {
		t.Fatalf("an empty body reported an error: %v", failure)
	}
	if target.Name != "unchanged" {
		t.Fatalf("the target was overwritten: %+v", target)
	}
}

func TestDecodeJSONReportsBrokenJSON(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":`))
	var target map[string]any
	if failure := DecodeJSON(request, &target); failure == nil {
		t.Fatal("broken JSON did not report an error")
	}
}

// The body is capped at 1 MiB, so a huge upload cannot be turned into memory
// pressure through a JSON endpoint.
func TestDecodeJSONTruncatesOversizedBody(t *testing.T) {
	oversized := `{"note":"` + strings.Repeat("a", 2<<20) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(oversized))
	var target map[string]any
	if failure := DecodeJSON(request, &target); failure == nil {
		t.Fatal("a body beyond the limit was decoded completely")
	}
}

func TestClientIPUsesRemoteAddressWithoutProxies(t *testing.T) {
	resetTrustedProxies(t)
	SetTrustedProxies(nil)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.9:41234"
	if address := ClientIP(request); address != "203.0.113.9" {
		t.Fatalf("unexpected address %q", address)
	}
}

// Without a configured proxy the header is attacker controlled: honouring it
// would let a client walk through the login rate limit with a fresh value per
// request.
func TestClientIPIgnoresHeaderFromUntrustedPeer(t *testing.T) {
	resetTrustedProxies(t)
	SetTrustedProxies(nil)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.9:41234"
	request.Header.Set("X-Real-IP", "10.1.2.3")
	if address := ClientIP(request); address != "203.0.113.9" {
		t.Fatalf("the header was believed: %q", address)
	}
}

func TestClientIPHonoursHeaderFromTrustedProxy(t *testing.T) {
	resetTrustedProxies(t)
	if invalid := SetTrustedProxies([]string{"172.18.0.0/16"}); len(invalid) != 0 {
		t.Fatalf("a valid CIDR was rejected: %v", invalid)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "172.18.0.5:41234"
	request.Header.Set("X-Real-IP", " 203.0.113.9 ")
	if address := ClientIP(request); address != "203.0.113.9" {
		t.Fatalf("the header of a trusted proxy was ignored: %q", address)
	}
}

func TestClientIPFallsBackWhenTrustedProxySendsGarbage(t *testing.T) {
	resetTrustedProxies(t)
	SetTrustedProxies([]string{"172.18.0.5"})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "172.18.0.5:41234"
	request.Header.Set("X-Real-IP", "not an address")
	if address := ClientIP(request); address != "172.18.0.5" {
		t.Fatalf("an unparsable header value was not discarded: %q", address)
	}
}

// X-Forwarded-For is deliberately ignored, including behind a trusted proxy.
func TestClientIPIgnoresForwardedFor(t *testing.T) {
	resetTrustedProxies(t)
	SetTrustedProxies([]string{"172.18.0.0/16"})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "172.18.0.5:41234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	if address := ClientIP(request); address != "172.18.0.5" {
		t.Fatalf("X-Forwarded-For was evaluated: %q", address)
	}
}

func TestClientIPWithoutPortInRemoteAddress(t *testing.T) {
	resetTrustedProxies(t)
	SetTrustedProxies(nil)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.113.9"
	if address := ClientIP(request); address != "203.0.113.9" {
		t.Fatalf("unexpected address %q", address)
	}
}

func TestClientIPWithoutAnyAddress(t *testing.T) {
	resetTrustedProxies(t)
	SetTrustedProxies(nil)

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = ""
	if address := ClientIP(request); address != "unknown" {
		t.Fatalf("unexpected address %q", address)
	}
}

func TestSetTrustedProxiesAcceptsPlainIPv4AndIPv6(t *testing.T) {
	resetTrustedProxies(t)
	if invalid := SetTrustedProxies([]string{"172.18.0.5", "2001:db8::1"}); len(invalid) != 0 {
		t.Fatalf("plain addresses were rejected: %v", invalid)
	}

	ipv4Request := httptest.NewRequest(http.MethodGet, "/", nil)
	ipv4Request.RemoteAddr = "172.18.0.5:41234"
	ipv4Request.Header.Set("X-Real-IP", "203.0.113.9")
	if address := ClientIP(ipv4Request); address != "203.0.113.9" {
		t.Fatalf("the IPv4 proxy is not trusted: %q", address)
	}

	ipv6Request := httptest.NewRequest(http.MethodGet, "/", nil)
	ipv6Request.RemoteAddr = "[2001:db8::1]:41234"
	ipv6Request.Header.Set("X-Real-IP", "203.0.113.9")
	if address := ClientIP(ipv6Request); address != "203.0.113.9" {
		t.Fatalf("the IPv6 proxy is not trusted: %q", address)
	}

	strangerRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	strangerRequest.RemoteAddr = "172.18.0.6:41234"
	strangerRequest.Header.Set("X-Real-IP", "203.0.113.9")
	if address := ClientIP(strangerRequest); address != "172.18.0.6" {
		t.Fatalf("a neighbouring address was trusted too: %q", address)
	}
}

func TestSetTrustedProxiesReportsInvalidEntries(t *testing.T) {
	resetTrustedProxies(t)
	invalid := SetTrustedProxies([]string{" 10.0.0.0/8 ", "", "   ", "not-an-address", "10.0.0.0/99"})

	if len(invalid) != 2 {
		t.Fatalf("expected 2 invalid entries, got %v", invalid)
	}
	if invalid[0] != "not-an-address" || invalid[1] != "10.0.0.0/99" {
		t.Fatalf("unexpected invalid entries %v", invalid)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.5.6.7:41234"
	request.Header.Set("X-Real-IP", "203.0.113.9")
	if address := ClientIP(request); address != "203.0.113.9" {
		t.Fatalf("the padded CIDR was not applied: %q", address)
	}
}

// Reconfiguring must replace the list, not add to it.
func TestSetTrustedProxiesReplacesPreviousList(t *testing.T) {
	resetTrustedProxies(t)
	SetTrustedProxies([]string{"172.18.0.0/16"})
	SetTrustedProxies([]string{"10.0.0.0/8"})

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "172.18.0.5:41234"
	request.Header.Set("X-Real-IP", "203.0.113.9")
	if address := ClientIP(request); address != "172.18.0.5" {
		t.Fatalf("the old entry is still active: %q", address)
	}
}
