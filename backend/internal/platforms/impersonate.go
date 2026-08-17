package platforms

import (
	"io"
	"net/url"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// browserSession sends requests with a real browser TLS/HTTP2 fingerprint (JA3)
// to pass Cloudflare "Managed Challenges" that the Go standard http client fails
// (403 "Just a moment"). Needed e.g. for the Bambu 2FA endpoint on bambulab.com
// (the website host is CF-protected, the API host is not).
//
// Unlike a one-shot request it keeps a cookie jar, so a flow that spans several
// calls sees the cookies the server set on the way. The Bambu 2FA endpoint needs
// exactly that: it hands out a CSRF cookie on one call and requires it on the
// next.
type browserSession struct {
	client tls_client.HttpClient
}

// newBrowserSession builds a session with an empty cookie jar.
func newBrowserSession() (*browserSession, error) {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Firefox_117),
		tls_client.WithNotFollowRedirects(),
		tls_client.WithCookieJar(tls_client.NewCookieJar()),
	}
	client, failure := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
	if failure != nil {
		return nil, failure
	}
	return &browserSession{client: client}, nil
}

// do sends one request through the session, storing every cookie the response
// sets. Returns: HTTP status, Set-Cookie values, body. On any error (0, nil, nil).
func (session *browserSession) do(method, rawURL, body string, headers map[string]string) (int, []string, []byte) {
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	request, failure := fhttp.NewRequest(method, rawURL, payload)
	if failure != nil {
		return 0, nil, nil
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, failure := session.client.Do(request)
	if failure != nil {
		recordRequest(rawURL, requestKindAPI, 0)
		return 0, nil, nil
	}
	defer response.Body.Close()
	recordRequest(rawURL, requestKindAPI, response.StatusCode)
	data, _ := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	return response.StatusCode, response.Header["Set-Cookie"], data
}

// cookie returns the value of a cookie the session collected for that URL, or ""
// when the server never set it.
func (session *browserSession) cookie(rawURL, name string) string {
	parsed, failure := url.Parse(rawURL)
	if failure != nil {
		return ""
	}
	for _, stored := range session.client.GetCookies(parsed) {
		if stored.Name == name {
			return stored.Value
		}
	}
	return ""
}
