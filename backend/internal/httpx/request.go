package httpx

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
)

// DecodeJSON reads the request body as JSON into target. An empty body is
// allowed and leaves target unchanged.
func DecodeJSON(request *http.Request, target any) error {
	body, failure := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if failure != nil {
		return failure
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, target)
}

// trustedProxies are the networks whose X-Real-IP header ClientIP believes.
// Empty by default: the standard deployment publishes the app port directly
// (docker-compose.yml), and there any client can set the header itself.
var trustedProxies []*net.IPNet

// SetTrustedProxies configures the reverse proxies whose X-Real-IP header is
// honoured. Entries are CIDRs ("10.0.0.0/8") or plain IPs ("172.18.0.5").
// Invalid entries are reported and ignored. Call once at startup.
func SetTrustedProxies(entries []string) []string {
	var networks []*net.IPNet
	var invalid []string
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if _, network, failure := net.ParseCIDR(entry); failure == nil {
			networks = append(networks, network)
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			networks = append(networks, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		invalid = append(invalid, entry)
	}
	trustedProxies = networks
	return invalid
}

// ClientIP determines the client IP. X-Real-IP is only believed when the request
// actually came from a configured trusted proxy - otherwise it is attacker
// controlled: with the app directly exposed, a client can send a fresh value per
// request and walk straight through the login rate limit. X-Forwarded-For is
// deliberately ignored entirely.
func ClientIP(request *http.Request) string {
	remote := remoteIP(request)
	if realIP := request.Header.Get("X-Real-IP"); realIP != "" && isTrustedProxy(remote) {
		if parsed := net.ParseIP(strings.TrimSpace(realIP)); parsed != nil {
			return parsed.String()
		}
	}
	if remote != "" {
		return remote
	}
	return "unknown"
}

// remoteIP is the peer address of the connection, without the port.
func remoteIP(request *http.Request) string {
	if host, _, failure := net.SplitHostPort(request.RemoteAddr); failure == nil {
		return host
	}
	return request.RemoteAddr
}

// isTrustedProxy reports whether address is one of the configured proxies.
func isTrustedProxy(address string) bool {
	if len(trustedProxies) == 0 {
		return false
	}
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}
	for _, network := range trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
