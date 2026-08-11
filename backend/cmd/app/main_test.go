package main

import (
	"strings"
	"testing"

	"meshdepot/internal/config"
)

// validConfig is the minimum the startup check accepts; each test breaks exactly
// one field of it.
func validConfig() config.Config {
	return config.Config{
		HTTPAddr: ":8080",
		AppKey:   "a-key-with-at-least-32-characters!",
	}
}

func TestValidateConfigAcceptsAKeyAndAnAddress(t *testing.T) {
	if failure := validateConfig(validConfig()); failure != nil {
		t.Fatalf("a complete configuration was rejected: %v", failure)
	}
}

func TestValidateConfigRejectsAMissingHTTPAddr(t *testing.T) {
	broken := validConfig()
	broken.HTTPAddr = ""

	failure := validateConfig(broken)
	if failure == nil {
		t.Fatal("an unset HTTP_ADDR was accepted - the app would listen on nothing")
	}
	// The message has to name the variable: it is all the operator sees in the log.
	if !strings.Contains(failure.Error(), "HTTP_ADDR") {
		t.Fatalf("the error does not name HTTP_ADDR: %v", failure)
	}
}

func TestValidateConfigRejectsTheDefaultAppKey(t *testing.T) {
	broken := validConfig()
	broken.AppKey = config.DefaultAppKey

	failure := validateConfig(broken)
	if failure == nil {
		t.Fatal("the built-in default APP_KEY was accepted")
	}
	if !strings.Contains(failure.Error(), "APP_KEY") {
		t.Fatalf("the error does not name APP_KEY: %v", failure)
	}
}
