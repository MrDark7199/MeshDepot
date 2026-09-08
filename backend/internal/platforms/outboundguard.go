package platforms

// Refusal to fetch addresses that only exist inside the network the server sits
// in.
//
// The browser import takes download URLs from the request body, and that is the
// difference from every other path here: elsewhere the addresses come from this
// package's own platform code, while these are supplied by whoever holds an API
// key. Without a guard, an account holder could point the server at
// http://192.168.1.1/, at a neighbouring container, or at a cloud provider's
// metadata service, and read the answer back out of their own library. On a
// machine reachable from the internet that is a way through the firewall.
//
// The check is at connect time, on the address actually dialled, rather than on
// the URL. That is deliberate: a hostname can resolve to a private address, it
// can resolve differently on the second lookup than on the first, and a public
// URL can redirect to a private one. Only the socket knows the truth.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// errPrivateAddress is returned when a fetch is refused for its destination.
var errPrivateAddress = errors.New("the address is inside a private network")

// AllowPrivateDownloadsForTest lifts the guard.
//
// It exists for the test suite, which serves its files from 127.0.0.1 - the very
// address the guard is there to refuse. Exported so the tests of other packages
// can set it, and named so that finding it switched on anywhere else is an
// obvious mistake. Nothing in the running application touches it.
var AllowPrivateDownloadsForTest = false

// isPrivateAddress reports whether an address belongs to the machine, its
// network, or one of the ranges that are never a public host.
func isPrivateAddress(address net.IP) bool {
	if address == nil {
		return true
	}
	if address.IsLoopback() || address.IsPrivate() || address.IsUnspecified() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsInterfaceLocalMulticast() || address.IsMulticast() {
		return true
	}
	// 100.64.0.0/10, the carrier-grade NAT range, and where Tailscale and
	// similar overlays live - a machine's neighbours by another name.
	if fourByte := address.To4(); fourByte != nil {
		if fourByte[0] == 100 && fourByte[1] >= 64 && fourByte[1] <= 127 {
			return true
		}
	}
	// fc00::/7, IPv6's private range, which IsPrivate covers, and IPv4-mapped
	// forms of everything above, which To4 above resolves.
	return false
}

// guardedDownloadClient is downloadClient with every connection checked.
//
// Redirects are followed as usual - the dialler sees each hop, so a public URL
// that redirects into the network is refused at the moment it would connect.
func guardedDownloadClient() *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, failure := net.SplitHostPort(address)
			if failure != nil {
				return nil, failure
			}
			// Already an address by this point: the resolver ran before the dial.
			// Resolving here again would leave a window where the answer changes
			// between the check and the connection.
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
			// Dialled by IP rather than by name, so the connection goes to an
			// address that was checked and not to whatever a second lookup
			// returns.
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
