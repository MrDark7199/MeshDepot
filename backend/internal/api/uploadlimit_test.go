package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// oversizedError produces the error a handler really sees when a request body
// runs past the limit, instead of building an http.MaxBytesError by hand.
func oversizedError(t *testing.T) error {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("mehr als erlaubt"))
	recorder := httptest.NewRecorder()
	limitRequestBody(recorder, request, 4)
	_, failure := io.ReadAll(request.Body)
	if failure == nil {
		t.Fatal("reading past the limit succeeded")
	}
	return failure
}

func TestBodyTooLargeRecognizesTheLimit(t *testing.T) {
	if !bodyTooLarge(oversizedError(t)) {
		t.Fatal("the limit error was not recognized")
	}
	if bodyTooLarge(errors.New("etwas anderes")) {
		t.Fatal("an unrelated error was taken for the limit")
	}
	if bodyTooLarge(nil) {
		t.Fatal("a missing error was taken for the limit")
	}
}

func TestUploadErrorSeparatesTheTooLargeCase(t *testing.T) {
	recorder := httptest.NewRecorder()
	uploadError(recorder, oversizedError(t), "error.upload_failed")
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized body answered %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "error.upload_too_large") {
		t.Fatalf("the answer carries %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	uploadError(recorder, errors.New("kaputtes multipart"), "error.upload_failed")
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a broken upload answered %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "error.upload_failed") {
		t.Fatalf("the answer carries %s", recorder.Body.String())
	}
}

// The limit has to hold on the real route as well, not only in the helper.
func TestAvatarUploadRejectsAnOversizedFile(t *testing.T) {
	testHarness := newHarness(t)
	oversized := make([]byte, maxAvatarUpload+1024)

	answer := testHarness.uploadAsUser(http.MethodPost,
		fmt.Sprintf("/api/v1/users/%s/avatar", testHarness.publicID(testHarness.userID)), "avatar", "gross.png", oversized)

	if answer.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized avatar answered %d: %s", answer.status, answer.rawBody)
	}
	if key := answer.errorKey(t); key != "error.upload_too_large" {
		t.Fatalf("unexpected error key %q", key)
	}
}
