package platforms

import (
	"testing"
	"time"
)

// TestRefreshToken pins the decision table that the five former copies of the
// token resolution disagreed on: when a login is attempted at all, what happens
// to the old token when it fails, and that the "***" placeholder is never sent
// as a password.
func TestRefreshToken(t *testing.T) {
	const platform = "testplatform"
	attempted := Credentials{}
	loginResult := "fresh-token"

	// A stub platform keeps the test off the network. TokenExpiry stays empty, so
	// nothing is persisted and no database is needed.
	original := All
	All = append(append([]Platform{}, All...), Platform{
		Name: platform, Label: "Test", Domain: "test.invalid",
		autoLogin: func(_ Deps, credentials Credentials) string {
			attempted = credentials
			return loginResult
		},
	})
	defer func() { All = original }()

	expired := time.Now().Add(-time.Hour).UTC().Format("2006-01-02 15:04:05")
	future := time.Now().Add(time.Hour).UTC().Format("2006-01-02 15:04:05")
	credentials := platformAccount{Username: "user@example.com", Password: "secret", Expires: expired}

	cases := []struct {
		name        string
		account     platformAccount
		loginResult string
		wantToken   string
		wantFailed  bool
		wantLogin   bool
	}{
		{name: "valid token is used as is",
			account:   platformAccount{Token: "stored", Username: "user@example.com", Password: "secret", Expires: future},
			wantToken: "stored"},
		{name: "expired token triggers a login",
			account:     platformAccount{Token: "stale", Username: "user@example.com", Password: "secret", Expires: expired},
			loginResult: "fresh-token", wantToken: "fresh-token", wantLogin: true},
		{name: "missing token triggers a login",
			account: credentials, loginResult: "fresh-token", wantToken: "fresh-token", wantLogin: true},
		{name: "failed login keeps the old token and reports it",
			account:     platformAccount{Token: "stale", Username: "user@example.com", Password: "secret", Expires: expired},
			loginResult: "", wantToken: "stale", wantFailed: true, wantLogin: true},
		{name: "masked password is never sent",
			account: platformAccount{Username: "user@example.com", Password: "***", Expires: expired}},
		{name: "no credentials, no login",
			account: platformAccount{Expires: expired}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			attempted, loginResult = Credentials{}, testCase.loginResult
			token, loginFailed := Deps{}.refreshToken(testCase.account, 1, platform, nil)
			if token != testCase.wantToken || loginFailed != testCase.wantFailed {
				t.Errorf("refreshToken = (%q, %v), want (%q, %v)", token, loginFailed, testCase.wantToken, testCase.wantFailed)
			}
			if attemptedLogin := attempted != (Credentials{}); attemptedLogin != testCase.wantLogin {
				t.Errorf("login attempted = %v, want %v (credentials %+v)", attemptedLogin, testCase.wantLogin, attempted)
			}
		})
	}
}

// TestAutoLoginMatchesTokenExpiry keeps the two halves of the auto-login
// configuration together: a login whose token has no lifetime would be fetched
// again on every download, a lifetime without a login is never used.
func TestAutoLoginMatchesTokenExpiry(t *testing.T) {
	for _, platform := range All {
		if (platform.autoLogin == nil) != (platform.TokenExpiry == "") {
			t.Errorf("%s: autoLogin set = %v, but TokenExpiry = %q", platform.Name, platform.autoLogin != nil, platform.TokenExpiry)
		}
	}
}
