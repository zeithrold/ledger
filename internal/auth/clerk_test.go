package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/go-jose/go-jose/v3"
	josejwt "github.com/go-jose/go-jose/v3/jwt"

	"github.com/zeithrold/ledger/internal/problem"
)

type sourceStub struct {
	set   *clerk.JSONWebKeySet
	err   error
	calls int
}

func (s *sourceStub) Get(context.Context, *jwks.GetParams) (*clerk.JSONWebKeySet, error) {
	s.calls++
	return s.set, s.err
}

func TestVerifySession(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	jwk := &clerk.JSONWebKey{Key: &private.PublicKey, KeyID: "test", Algorithm: "RS256", Use: "sig"}
	source := &sourceStub{set: &clerk.JSONWebKeySet{Keys: []*clerk.JSONWebKey{jwk}}}
	v := &Clerk{issuer: "https://clerk.example.com", parties: []string{"https://app.example.com"}, source: source, now: time.Now}
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{"valid", func(map[string]any) {}, true},
		{"native without azp", func(c map[string]any) { delete(c, "azp") }, true},
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://clerk.attacker.com" }, false},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }, false},
		{"future", func(c map[string]any) { c["nbf"] = time.Now().Add(time.Minute).Unix() }, false},
		{"missing exp", func(c map[string]any) { delete(c, "exp") }, false},
		{"missing nbf", func(c map[string]any) { delete(c, "nbf") }, false},
		{"missing subject", func(c map[string]any) { delete(c, "sub") }, false},
		{"not session", func(c map[string]any) { delete(c, "sid") }, false},
		{"pending", func(c map[string]any) { c["sts"] = "pending" }, false},
		{"wrong azp", func(c map[string]any) { c["azp"] = "https://attacker.com" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := map[string]any{"iss": v.issuer, "sub": "user_test", "sid": "sess_test", "exp": time.Now().Add(time.Minute).Unix(), "nbf": time.Now().Add(-time.Minute).Unix(), "azp": v.parties[0]}
			tc.change(claims)
			token := signToken(t, private, "test", claims)
			who, verifyErr := v.Verify(t.Context(), token)
			if tc.valid {
				if verifyErr != nil || who.Subject != "user_test" {
					t.Fatalf("identity=%v error=%v", who, verifyErr)
				}
			} else {
				assertProblem(t, verifyErr, problem.InvalidToken)
			}
		})
	}
	if source.calls != 1 {
		t.Fatalf("cache fetched %d times", source.calls)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	token := signToken(t, other, "test", map[string]any{"iss": v.issuer})
	_, err = v.Verify(t.Context(), token)
	assertProblem(t, err, problem.InvalidToken)
	_, err = v.Verify(t.Context(), "malformed")
	assertProblem(t, err, problem.InvalidToken)
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", kid))
	if err != nil {
		t.Fatal(err)
	}
	token, err := josejwt.Signed(signer).Claims(claims).CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func assertProblem(t *testing.T, err error, kind problem.Kind) {
	t.Helper()
	var p *problem.Error
	if !errors.As(err, &p) || p.Kind != kind {
		t.Fatalf("error=%v, want %s", err, kind)
	}
}

func TestKeyCacheRotationAndFailure(t *testing.T) {
	now := time.Now()
	source := &sourceStub{set: &clerk.JSONWebKeySet{Keys: []*clerk.JSONWebKey{{KeyID: "a", Algorithm: "RS256"}}}}
	v := &Clerk{source: source, now: func() time.Time { return now }}
	if _, err := v.key(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	source.set.Keys = append(source.set.Keys, &clerk.JSONWebKey{KeyID: "b", Algorithm: "RS256"})
	if _, err := v.key(t.Context(), "b"); err != nil {
		t.Fatal(err)
	}
	_, err := v.key(t.Context(), "unknown")
	assertProblem(t, err, problem.InvalidToken)
	if source.calls != 2 {
		t.Fatalf("unknown kid caused fetch storm: %d", source.calls)
	}
	now = now.Add(2 * time.Hour)
	source.err = errors.New("secret upstream failure")
	_, err = v.key(t.Context(), "a")
	assertProblem(t, err, problem.Unavailable)
	_, err = v.key(t.Context(), "a")
	assertProblem(t, err, problem.Unavailable)
	if source.calls != 3 {
		t.Fatal("failure cooldown not applied")
	}
	now = now.Add(31 * time.Second)
	source.err = nil
	if _, err = v.key(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
}
