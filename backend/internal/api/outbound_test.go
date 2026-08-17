package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHttpGetSendsTheHeadersAndReturnsTheBody(t *testing.T) {
	var seenAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		seenAuthorization = request.Header.Get("Authorization")
		fmt.Fprint(responseWriter, `{"ok":true}`)
	}))
	defer server.Close()

	body := httpGet(server.URL, map[string]string{"Authorization": "Bearer geheim"})

	if body != `{"ok":true}` {
		t.Fatalf("the answer carries %q", body)
	}
	if seenAuthorization != "Bearer geheim" {
		t.Fatalf("the server saw the authorization %q", seenAuthorization)
	}
}

func TestHttpPostJSONSendsTheBody(t *testing.T) {
	var seenBody, seenContentType, seenMethod string
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		seenMethod = request.Method
		seenContentType = request.Header.Get("Content-Type")
		raw, _ := io.ReadAll(request.Body)
		seenBody = string(raw)
		fmt.Fprint(responseWriter, `{"data":null}`)
	}))
	defer server.Close()

	answer := httpPostJSON(server.URL, `{"query":"{print(id:1){id}}"}`)

	if answer != `{"data":null}` {
		t.Fatalf("the answer carries %q", answer)
	}
	if seenMethod != http.MethodPost || seenContentType != "application/json" {
		t.Fatalf("the server saw %s with the content type %q", seenMethod, seenContentType)
	}
	if seenBody != `{"query":"{print(id:1){id}}"}` {
		t.Fatalf("the server saw the body %q", seenBody)
	}
}

func TestHttpGetBytesReturnsTheRawBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		_, _ = responseWriter.Write([]byte{0xff, 0xd8, 0xff, 0x00})
	}))
	defer server.Close()

	if data := httpGetBytes(server.URL); !bytes.Equal(data, []byte{0xff, 0xd8, 0xff, 0x00}) {
		t.Fatalf("the answer carries %v", data)
	}
}

// A platform that is unreachable must not take the handler down with it; the
// fetch simply comes back empty.
func TestHttpDoReturnsNothingOnAFailedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachableURL := server.URL
	server.Close()

	if data := httpDo(unreachableURL, http.MethodGet, "", nil); data != nil {
		t.Fatalf("an unreachable server returned %q", data)
	}
	if data := httpDo("://kaputt", http.MethodGet, "", nil); data != nil {
		t.Fatalf("an invalid url returned %q", data)
	}
}
