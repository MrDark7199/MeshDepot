package platforms

// Refusal to fetch addresses that only exist inside the network the server sits
// in.
//
// The browser import takes download URLs from the request body: elsewhere the
// addresses come from this package's own platform code, while these are supplied
// by whoever holds an API key. Without a guard an account holder could point the
// server at http://192.168.1.1/, at a neighbouring container, or at a cloud
// metadata service, and read the answer back out of their own library.
//
// The check is at connect time, on the address actually dialled: a hostname can
// resolve to a private address, resolve differently on the second lookup, or
// redirect to a private one. Only the socket knows the truth.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

var errPrivateAddress = errors.New("the address is inside a private network")

// AllowPrivateDownloadsForTest lifts the guard for the test suite, which serves
// its files from 127.0.0.1 - the very address the guard refuses. Named so that
// finding it switched on anywhere else is an obvious mistake.
var AllowPrivateDownloadsForTest = false

// isPrivateAddress covers the machine, its network, and the ranges that are never
// a public host.
func isPrivateAddress(address net.IP) bool {
	if address == nil {
		return true
	}
	if address.IsLoopback() || address.IsPrivate() || address.IsUnspecified() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsInterfaceLocalMulticast() || address.IsMulticast() {
		return true
	}
	// 100.64.0.0/10, carrier-grade NAT, where Tailscale and similar overlays live.
	if fourByte := address.To4(); fourByte != nil {
		if fourByte[0] == 100 && fourByte[1] >= 64 && fourByte[1] <= 127 {
			return true
		}
	}
	// fc00::/7 is covered by IsPrivate, and IPv4-mapped forms by To4 above.
	return false
}

// guardedDownloadClient is downloadClient with every connection checked.
// Redirects are followed as usual: the dialler sees each hop, so a public URL
// redirecting into the network is refused where it would connect.
func guardedDownloadClient() *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, failure := net.SplitHostPort(address)
			if failure != nil {
				return nil, failure
			}
			// Already an address here - resolving again would leave a window where the
			// answer changes between the check and the connection.
			resolved, failure := dialer.Resolver.LookupIPAddr(ctx, host)
			if failure != nil {
				return nil, failure
			}
			if !AllowPrivateDownloadsForTest {
				for _, candidate := range resolved {
					if isPrivateAddress(candidate.IP) {
						return nil, errPrivateAddress
					}
				}
			}
			// Dialled by IP, so the connection goes to the address that was checked.
			var lastFailure error
			for _, candidate := range resolved {
				connection, failure := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
				if failure == nil {
					return connection, nil
				}
				lastFailure = failure
			}
			return nil, lastFailure
		},
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	return &http.Client{Timeout: downloadTimeout, Transport: transport}
}
