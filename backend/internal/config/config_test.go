package config

import (
	"reflect"
	"testing"
)

func TestLoadUsesDefaultsWhenNothingIsSet(t *testing.T) {
	for _, key := range []string{
		"HTTP_ADDR", "APP_KEY", "APP_HTTPS", "APP_URL", "DB_PATH",
		"BASE_PATH_DATA", "CHROMIUM_BIN", "TOR_BIN",
		"LOG_LEVEL", "PLAYWRIGHT_URL", "PLAYWRIGHT_TOKEN", "TRUSTED_PROXIES",
	} {
		t.Setenv(key, "")
	}

	loaded := Load()

	expected := Config{
		HTTPAddr:        "",
		AppKey:          DefaultAppKey,
		AppHTTPS:        false,
		AppURL:          "",
		DBPath:          "/data/meshdepot.db",
		BasePathData:    "/data",
		ChromiumBin:     "/usr/bin/chromium",
		TorBin:          "tor",
		LogLevel:        "all",
		PlaywrightURL:   "",
		PlaywrightToken: "",
		TrustedProxies:  nil,
	}
	if !reflect.DeepEqual(loaded, expected) {
		t.Fatalf("defaults changed:\n got %+v\nwant %+v", loaded, expected)
	}
}

func TestLoadReadsEveryVariable(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9000")
	t.Setenv("APP_KEY", "a-key-with-at-least-32-characters!")
	t.Setenv("APP_HTTPS", "true")
	t.Setenv("APP_URL", "https://depot.example.org")
	t.Setenv("DB_PATH", "/srv/meshdepot.db")
	t.Setenv("BASE_PATH_DATA", "/srv/meshdepot")
	t.Setenv("CHROMIUM_BIN", "/opt/chromium")
	t.Setenv("TOR_BIN", "/opt/tor")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("PLAYWRIGHT_URL", "http://playwright:3000")
	t.Setenv("PLAYWRIGHT_TOKEN", "shared-secret")
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8, 172.18.0.5")

	loaded := Load()

	expected := Config{
		HTTPAddr:        ":9000",
		AppKey:          "a-key-with-at-least-32-characters!",
		AppHTTPS:        true,
		AppURL:          "https://depot.example.org",
		DBPath:          "/srv/meshdepot.db",
		BasePathData:    "/srv/meshdepot",
		ChromiumBin:     "/opt/chromium",
		TorBin:          "/opt/tor",
		LogLevel:        "error",
		PlaywrightURL:   "http://playwright:3000",
		PlaywrightToken: "shared-secret",
		TrustedProxies:  []string{"10.0.0.0/8", "172.18.0.5"},
	}
	if !reflect.DeepEqual(loaded, expected) {
		t.Fatalf("a variable was not read:\n got %+v\nwant %+v", loaded, expected)
	}
}

func TestEnvFallsBackOnEmptyValue(t *testing.T) {
	t.Setenv("MESHDEPOT_TEST_VALUE", "")
	if value := env("MESHDEPOT_TEST_VALUE", "fallback"); value != "fallback" {
		t.Fatalf("an empty variable did not fall back: %q", value)
	}
	t.Setenv("MESHDEPOT_TEST_VALUE", "set")
	if value := env("MESHDEPOT_TEST_VALUE", "fallback"); value != "set" {
		t.Fatalf("the set value was ignored: %q", value)
	}
}

func TestEnvBoolAcceptsBothSpellings(t *testing.T) {
	trueValues := []string{"1", "t", "T", "true", "TRUE", "True"}
	for _, raw := range trueValues {
		t.Setenv("MESHDEPOT_TEST_FLAG", raw)
		if !envBool("MESHDEPOT_TEST_FLAG", false) {
			t.Fatalf("%q was not read as true", raw)
		}
	}
	falseValues := []string{"0", "f", "false", "FALSE"}
	for _, raw := range falseValues {
		t.Setenv("MESHDEPOT_TEST_FLAG", raw)
		if envBool("MESHDEPOT_TEST_FLAG", true) {
			t.Fatalf("%q was not read as false", raw)
		}
	}
	// Spellings that strconv.ParseBool rejects are handled by the fallback.
	for _, raw := range []string{"yes", "on"} {
		t.Setenv("MESHDEPOT_TEST_FLAG", raw)
		if !envBool("MESHDEPOT_TEST_FLAG", false) {
			t.Fatalf("%q was not read as true", raw)
		}
	}
	t.Setenv("MESHDEPOT_TEST_FLAG", "maybe")
	if envBool("MESHDEPOT_TEST_FLAG", true) {
		t.Fatal("an unparsable value was not read as false")
	}
	t.Setenv("MESHDEPOT_TEST_FLAG", "")
	if !envBool("MESHDEPOT_TEST_FLAG", true) {
		t.Fatal("an empty variable did not fall back to the default")
	}
}

func TestEnvListTrimsAndDropsEmptyEntries(t *testing.T) {
	t.Setenv("MESHDEPOT_TEST_LIST", "  10.0.0.0/8 ,, 172.18.0.5 , ")
	entries := envList("MESHDEPOT_TEST_LIST")
	if !reflect.DeepEqual(entries, []string{"10.0.0.0/8", "172.18.0.5"}) {
		t.Fatalf("unexpected entries %#v", entries)
	}

	t.Setenv("MESHDEPOT_TEST_LIST", "   ")
	if entries := envList("MESHDEPOT_TEST_LIST"); entries != nil {
		t.Fatalf("a blank value produced %#v instead of nil", entries)
	}
}

// HTTP_ADDR has no fallback: the deployment owns the listen address (Compose
// derives it, the port mapping and the healthcheck from one port), and main.go
// refuses to start on an empty value. A default here would silently bring the app
// up on a port nothing is mapped to.
func TestHTTPAddrHasNoDefault(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	if addr := Load().HTTPAddr; addr != "" {
		t.Fatalf("HTTP_ADDR fell back to %q instead of staying empty", addr)
	}
}

// The default key is the one main.go refuses to start on, so its exact value is
// part of the contract.
func TestDefaultAppKeyIsLongEnoughForAES256(t *testing.T) {
	if len(DefaultAppKey) < 32 {
		t.Fatalf("the default key is only %d bytes long", len(DefaultAppKey))
	}
}
