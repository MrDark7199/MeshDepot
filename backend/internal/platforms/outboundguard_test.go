package platforms

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The ranges that must never be reachable through a client-supplied URL. Getting
// this wrong turns the server into a way through its own firewall: an account
// holder could read a neighbouring container, a router's interface, or a cloud
// provider's metadata service, and collect the answer from their own library.
func TestPrivateAddressesAreRecognised(t *testing.T) {
	private := []string{
		"127.0.0.1", "::1", // the machine itself
		"10.0.0.5", "172.16.4.1", "192.168.178.62", // private networks
		"169.254.169.254",    // the cloud metadata address
		"100.64.0.1",         // carrier-grade NAT, where overlays live
		"0.0.0.0", "fc00::1", // unspecified, IPv6 private
		"224.0.0.1", // multicast
	}
	for _, address := range private {
		if !isPrivateAddress(net.ParseIP(address)) {
			t.Errorf("%s should be refused", address)
		}
	}

	public := []string{"1.1.1.1", "93.184.216.34", "2606:4700::1111"}
	for _, address := range public {
		if isPrivateAddress(net.ParseIP(address)) {
			t.Errorf("%s should be allowed", address)
		}
	}
}

// An unparsable address counts as private. Refusing what cannot be judged is the
// safe direction here.
func TestUnknownAddressIsRefused(t *testing.T) {
	if !isPrivateAddress(nil) {
		t.Fatal("an address that could not be parsed must be refused")
	}
}

// The guard sits at the socket rather than at the URL, so a loopback server is
// unreachable however it is addressed.
func TestGuardedClientRefusesLoopback(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		responseWriter.Write([]byte("secret"))
	}))
	defer origin.Close()

	// The seam the api tests use must not hide the guard from its own test.
	AllowPrivateDownloadsForTest = false
	if _, failure := guardedDownloadClient().Get(origin.URL); failure == nil {
		t.Fatal("a loopback address was fetched anyway")
	}

	// The same server through the ordinary client, to show the test server itself
	// is reachable and the refusal came from the guard.
	response, failure := downloadClient().Get(origin.URL)
	if failure != nil {
		t.Fatalf("the unguarded client should reach it: %v", failure)
	}
	response.Body.Close()
}
