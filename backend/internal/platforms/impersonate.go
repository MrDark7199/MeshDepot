package platforms

import (
	"io"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// browserPost sends a POST with a real browser TLS/HTTP2 fingerprint (JA3) to
// pass Cloudflare "Managed Challenges" that the Go standard http client fails
// (403 "Just a moment"). Needed e.g. for the Bambu 2FA endpoint on bambulab.com
// (the website host is CF-protected, the API host is not).
//
// Returns: HTTP status, Set-Cookie values, body. On any error (0, nil, nil).
func browserPost(url, body string, headers map[string]string) (int, []string, []byte) {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Firefox_117),
		tls_client.WithNotFollowRedirects(),
	}
	client, failure := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if failure != nil {
		return 0, nil, nil
	}
	request, failure := fhttp.NewRequest(fhttp.MethodPost, url, strings.NewReader(body))
	if failure != nil {
		return 0, nil, nil
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, failure := client.Do(request)
	if failure != nil {
		return 0, nil, nil
	}
	defer response.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	return response.StatusCode, response.Header["Set-Cookie"], data
}
