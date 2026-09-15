// Package auth verifies external identities without creating local users.
package auth

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/clerk/clerk-sdk-go/v2"
	"github.com/clerk/clerk-sdk-go/v2/jwks"
	"github.com/clerk/clerk-sdk-go/v2/jwt"

	"github.com/zeithrold/ledger/internal/config"
	"github.com/zeithrold/ledger/internal/problem"
)

// Identity is trusted only after verification of the session token.
type Identity struct {
	Issuer  string
	Subject string
}

// Verifier is the authentication boundary for HTTP and deterministic tests.
type Verifier interface {
	Verify(context.Context, string) (Identity, error)
}

type keySource interface {
	Get(context.Context, *jwks.GetParams) (*clerk.JSONWebKeySet, error)
}

// Clerk verifies tokens with the SDK and an instance-scoped, bounded-lifetime JWKS cache.
// SDK middleware's global sliding cache cannot distinguish key-fetch failures from bad tokens.
type Clerk struct {
	issuer        string
	parties       []string
	source        keySource
	mu            sync.Mutex
	keys          map[string]*clerk.JSONWebKey
	expires       time.Time
	refreshAfter  time.Time
	refreshFailed bool
	now           func() time.Time
}

// New constructs a verifier; only configured endpoints can receive the backend key.
func New(cfg config.Clerk) (*Clerk, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	source := jwks.NewClient(&clerk.ClientConfig{BackendConfig: clerk.BackendConfig{
		URL: clerk.String(strings.TrimRight(cfg.APIEndpoint, "/") + "/v1"), Key: clerk.String(cfg.SecretKey), HTTPClient: client,
	}})
	return &Clerk{issuer: cfg.IssuerURL, parties: slices.Clone(cfg.AuthorizedParties), source: source, now: time.Now}, nil
}

func (v *Clerk) key(ctx context.Context, kid string) (*clerk.JSONWebKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.now()
	if k := v.keys[kid]; k != nil && now.Before(v.expires) {
		return k, nil
	}
	if now.Before(v.refreshAfter) {
		if v.refreshFailed {
			return nil, problem.New(problem.Unavailable, "Authentication keys are temporarily unavailable.")
		}
		return nil, problem.New(problem.InvalidToken, "The session token is invalid.")
	}
	set, err := v.source.Get(ctx, &jwks.GetParams{})
	v.refreshAfter = now.Add(30 * time.Second)
	v.refreshFailed = err != nil || set == nil
	if v.refreshFailed {
		return nil, problem.New(problem.Unavailable, "Authentication keys are temporarily unavailable.")
	}
	keys := make(map[string]*clerk.JSONWebKey)
	for _, k := range set.Keys {
		if k != nil && k.Algorithm == "RS256" {
			keys[k.KeyID] = k
		}
	}
	v.keys = keys
	v.expires = now.Add(time.Hour)
	if k := keys[kid]; k != nil {
		return k, nil
	}
	return nil, problem.New(problem.InvalidToken, "The session token is invalid.")
}

// Match Clerk's documented five-second allowance for server clock differences.
const sessionClockSkew = 5 * time.Second

type verificationClock func() time.Time

func (c verificationClock) Now() time.Time { return c() }

// Verify accepts session tokens only; unverified claims are never used for authorization or URLs.
func (v *Clerk) Verify(ctx context.Context, token string) (Identity, error) {
	invalid := problem.New(problem.InvalidToken, "The session token is invalid.")
	decoded, err := jwt.Decode(ctx, &jwt.DecodeParams{Token: token})
	if err != nil || decoded.KeyID == "" {
		return Identity{}, invalid
	}
	key, err := v.key(ctx, decoded.KeyID)
	if err != nil {
		return Identity{}, err
	}
	custom := struct {
		Status string `json:"sts"`
	}{}
	claims, err := jwt.Verify(ctx, &jwt.VerifyParams{
		Token: token, JWK: key, ProxyURL: &v.issuer,
		Clock: verificationClock(v.now), Leeway: sessionClockSkew,
		CustomClaimsConstructor: func(context.Context) any { return &custom },
		AuthorizedPartyHandler:  func(azp string) bool { return azp == "" || slices.Contains(v.parties, azp) },
	})
	if err != nil {
		return Identity{}, invalid
	}
	if claims.Issuer != v.issuer || strings.TrimSpace(claims.Subject) == "" || strings.TrimSpace(claims.SessionID) == "" ||
		claims.Expiry == nil || claims.NotBefore == nil || custom.Status == "pending" {
		return Identity{}, invalid
	}
	return Identity{Issuer: claims.Issuer, Subject: claims.Subject}, nil
}
