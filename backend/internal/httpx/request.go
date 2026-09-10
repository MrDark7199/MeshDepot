package httpx

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
)

// DecodeJSON reads the body as JSON into target. An empty body is allowed and
// leaves target unchanged.
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
// Empty by default: the standard deployment publishes the app port directly, and
// there any client can set the header itself.
var trustedProxies []*net.IPNet

// SetTrustedProxies takes CIDRs or plain IPs; invalid entries are reported and
// ignored. Call once at startup.
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

// ClientIP believes X-Real-IP only from a configured trusted proxy - otherwise it
// is attacker controlled, and a client could send a fresh value per request and
// walk through the login rate limit. X-Forwarded-For is ignored entirely.
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

func remoteIP(request *http.Request) string {
	if host, _, failure := net.SplitHostPort(request.RemoteAddr); failure == nil {
		return host
	}
	return request.RemoteAddr
}

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

// IsSecureConnection reports whether the request arrived over a connection nobody
// on the way could read. Behind a reverse proxy the last hop is plaintext by
// design, so the proxy's X-Forwarded-Proto is used - but only from one the
// operator configured as trusted, since otherwise it is the sender's own claim.
func IsSecureConnection(request *http.Request) bool {
	if request.TLS != nil {
		return true
	}
	if !isTrustedProxy(remoteIP(request)) {
		return false
	}
	forwarded := strings.ToLower(strings.TrimSpace(request.Header.Get("X-Forwarded-Proto")))
	// A proxy may list the whole chain: "https, http".
	if comma := strings.Index(forwarded, ","); comma >= 0 {
		forwarded = strings.TrimSpace(forwarded[:comma])
	}
	return forwarded == "https"
}

// IsLocalClient reports whether the peer is this machine or its own network,
// where a plaintext connection crosses nothing an outsider could listen on - the
// ordinary case for a self-hosted MeshDepot.
func IsLocalClient(request *http.Request) bool {
	address := net.ParseIP(ClientIP(request))
	if address == nil {
		return false
	}
	return address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast()
}
