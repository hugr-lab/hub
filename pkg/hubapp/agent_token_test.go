package hubapp

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	qeauth "github.com/hugr-lab/query-engine/pkg/auth"
)

// fakeDirectory is an in-memory agentTokenDirectory.
type fakeDirectory struct {
	agents    map[string]agentTokenInfo
	secrets   map[string]string // secret_hash → agent_id, deleted on redeem
	lookupErr error
}

func (f *fakeDirectory) agentForToken(_ context.Context, agentID string) (agentTokenInfo, error) {
	if f.lookupErr != nil {
		return agentTokenInfo{}, f.lookupErr
	}
	info, ok := f.agents[agentID]
	if !ok {
		return agentTokenInfo{}, errors.New("agent not registered")
	}
	return info, nil
}

func (f *fakeDirectory) redeemBootstrapSecret(_ context.Context, secretHash string) (string, error) {
	id, ok := f.secrets[secretHash]
	if !ok {
		return "", errors.New("bootstrap secret unknown or consumed")
	}
	delete(f.secrets, secretHash) // one-shot
	return id, nil
}

// newTestIssuer generates an ECDSA P-256 key on disk and builds the issuer.
func newTestIssuer(t *testing.T, dir agentTokenDirectory, ttl time.Duration) *agentTokenIssuer {
	t.Helper()
	keyPath := writeTestKey(t)
	iss, err := newAgentTokenIssuer(Config{
		AgentJWTKeyFile: keyPath,
		AgentJWTIssuer:  "hub-agents",
		AgentTokenTTL:   ttl,
	}, dir, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatalf("newAgentTokenIssuer: %v", err)
	}
	return iss
}

func writeTestKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "agent-jwt.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func postToken(t *testing.T, iss *agentTokenIssuer, credential string) (*httptest.ResponseRecorder, tokenExchangeResponse) {
	t.Helper()
	body, _ := json.Marshal(tokenExchangeRequest{Token: credential})
	req := httptest.NewRequest(http.MethodPost, "/agent/token", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	iss.handleToken(rec, req)
	var resp tokenExchangeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

// Happy path: bootstrap secret → first JWT → refresh with that JWT → next
// JWT. The one-shot secret is dead after the first redeem.
func TestAgentToken_BootstrapThenRefresh(t *testing.T) {
	secret, err := newBootstrapSecret()
	if err != nil {
		t.Fatal(err)
	}
	dir := &fakeDirectory{
		agents:  map[string]agentTokenInfo{"agt-1": {ID: "agt-1", Name: "analyst", Role: "agent", Status: "active"}},
		secrets: map[string]string{hashBootstrapSecret(secret): "agt-1"},
	}
	iss := newTestIssuer(t, dir, 30*time.Minute)

	rec, resp := postToken(t, iss, secret)
	if rec.Code != http.StatusOK || resp.AccessToken == "" {
		t.Fatalf("bootstrap redeem: code=%d resp=%+v", rec.Code, resp)
	}
	if resp.ExpiresIn != int((30 * time.Minute).Seconds()) {
		t.Fatalf("expires_in = %d, want 1800", resp.ExpiresIn)
	}

	// Refresh with the issued JWT.
	rec2, resp2 := postToken(t, iss, resp.AccessToken)
	if rec2.Code != http.StatusOK || resp2.AccessToken == "" {
		t.Fatalf("refresh: code=%d resp=%+v", rec2.Code, resp2)
	}

	// The secret is one-shot.
	rec3, _ := postToken(t, iss, secret)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("second redeem: code=%d, want 401", rec3.Code)
	}
}

// The §1.5 core property: an EXPIRED issued JWT is still a valid refresh
// credential (signature is the proof; expiry deliberately ignored).
func TestAgentToken_ExpiredJWTStillRefreshes(t *testing.T) {
	dir := &fakeDirectory{
		agents: map[string]agentTokenInfo{"agt-1": {ID: "agt-1", Role: "agent", Status: "active"}},
	}
	expiredIss := newTestIssuer(t, dir, -time.Minute) // exp already in the past
	expired, _, err := expiredIss.issue(dir.agents["agt-1"])
	if err != nil {
		t.Fatal(err)
	}

	rec, resp := postToken(t, expiredIss, expired)
	if rec.Code != http.StatusOK || resp.AccessToken == "" {
		t.Fatalf("expired-JWT refresh: code=%d resp=%+v", rec.Code, resp)
	}
}

// Revocation: status != active denies the refresh (403), and a store
// failure is 503 — NOT a credential rejection, which hugen treats as fatal.
func TestAgentToken_StatusAndStoreGates(t *testing.T) {
	dir := &fakeDirectory{
		agents: map[string]agentTokenInfo{"agt-1": {ID: "agt-1", Role: "agent", Status: "disabled"}},
	}
	iss := newTestIssuer(t, dir, 30*time.Minute)
	tok, _, err := iss.issue(agentTokenInfo{ID: "agt-1", Role: "agent"})
	if err != nil {
		t.Fatal(err)
	}

	rec, _ := postToken(t, iss, tok)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled agent: code=%d, want 403", rec.Code)
	}

	dir.lookupErr = errors.New("db down")
	rec2, _ := postToken(t, iss, tok)
	if rec2.Code != http.StatusServiceUnavailable {
		t.Fatalf("store failure: code=%d, want 503", rec2.Code)
	}
}

// A token signed by a different key is rejected; so is a JWT from another
// issuer name signed with OUR key (defence against cross-issuer reuse).
func TestAgentToken_ForeignTokensRejected(t *testing.T) {
	dir := &fakeDirectory{
		agents: map[string]agentTokenInfo{"agt-1": {ID: "agt-1", Role: "agent", Status: "active"}},
	}
	iss := newTestIssuer(t, dir, 30*time.Minute)
	foreign := newTestIssuer(t, dir, 30*time.Minute) // different key
	foreignTok, _, err := foreign.issue(agentTokenInfo{ID: "agt-1", Role: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := postToken(t, iss, foreignTok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("foreign-key token: code=%d, want 401", rec.Code)
	}

	if _, err := iss.verifyRefreshCredential(foreignTok); err == nil {
		t.Fatal("verifyRefreshCredential accepted a foreign-key token")
	}
}

// The whole point of reusing query-engine/pkg/auth: a token we issue MUST
// verify through hugr's own JwtProvider configured with our published
// public key — same code path a real hugr deployment runs.
func TestAgentToken_HugrVerifiesIssuedToken(t *testing.T) {
	dir := &fakeDirectory{
		agents: map[string]agentTokenInfo{"agt-1": {ID: "agt-1", Name: "analyst", Role: "agent", Status: "active"}},
	}
	iss := newTestIssuer(t, dir, 30*time.Minute)
	tok, _, err := iss.issue(dir.agents["agt-1"])
	if err != nil {
		t.Fatal(err)
	}

	// The PEM exactly as GET /agent/token/public-key serves it.
	rec := httptest.NewRecorder()
	iss.handlePublicKey(rec, httptest.NewRequest(http.MethodGet, "/agent/token/public-key", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("public-key endpoint: %d", rec.Code)
	}

	provider, err := qeauth.NewJwt(&qeauth.JwtConfig{
		Issuer:    "hub-agents",
		PublicKey: rec.Body.String(),
	})
	if err != nil {
		t.Fatalf("hugr NewJwt with published key: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/query", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	info, err := provider.Authenticate(req)
	if err != nil {
		t.Fatalf("hugr Authenticate: %v", err)
	}
	if info.UserId != "agt-1" || info.Role != "agent" || info.UserName != "analyst" {
		t.Fatalf("hugr auth info = %+v, want user_id=agt-1 role=agent name=analyst", info)
	}
}

func TestAgentToken_BadRequests(t *testing.T) {
	dir := &fakeDirectory{agents: map[string]agentTokenInfo{}}
	iss := newTestIssuer(t, dir, 30*time.Minute)

	req := httptest.NewRequest(http.MethodGet, "/agent/token", nil)
	rec := httptest.NewRecorder()
	iss.handleToken(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: code=%d, want 405", rec.Code)
	}

	rec2, _ := postToken(t, iss, "")
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("empty token: code=%d, want 400", rec2.Code)
	}

	// Unknown bootstrap secret (not JWT-shaped) → 401.
	rec3, _ := postToken(t, iss, "deadbeef")
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("unknown secret: code=%d, want 401", rec3.Code)
	}
}
