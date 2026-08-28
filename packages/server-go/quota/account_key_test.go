package quota

import "testing"

func TestIdentityKey_TokenLoginFallsBackToAuthMethod(t *testing.T) {
	id := Identity{LoggedIn: true, AuthMethod: "oauth_token", APIProvider: "firstParty"}
	if got := id.Key(); got != "auth:oauth_token" {
		t.Fatalf("token login key = %q, want auth:oauth_token", got)
	}
	if got := (Identity{}).Key(); got != "unknown" {
		t.Fatalf("logged-out key = %q, want unknown", got)
	}
	if got := (Identity{LoggedIn: true, AuthMethod: "oauth_token", OrgID: "o1"}).Key(); got != "org:o1" {
		t.Fatalf("org wins: %q", got)
	}
}
