package hubapp

// Agent token authority (spec-hub-side §1, HB1) — hub-service issues the
// JWTs agents present to hugr; hugr verifies them against this issuer's
// public key (a static `jwt:` entry in hugr's auth config).
//
//   POST /agent/token          {token} → {access_token, expires_in}
//   GET  /agent/token/public-key   PEM for the hugr auth-config entry
//
// The presented credential is EITHER a previously-issued agent JWT — whose
// SIGNATURE is verified but whose expiry is deliberately ignored: possession
// of a signature-valid token is itself the refresh credential (§1.2) — OR a
// one-shot bootstrap secret minted at container spawn (§1.5). Every refresh
// passes the status gate (agents.status == 'active' in hub.agent.db), which
// makes this endpoint the hub's single control point: revocation latency is
// bounded by the token TTL. The L2 budget gate lands here too, once L1
// usage aggregation exists.
//
// Signing/verification reuse query-engine's pkg/auth (GenerateToken /
// ParsePrivateKey) so issue and hugr-side verify are guaranteed symmetric.

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hugr-lab/query-engine/client/app"
	qeauth "github.com/hugr-lab/query-engine/pkg/auth"
	"github.com/hugr-lab/query-engine/types"
)

// agentTokenInfo is what the issuer needs to know about an agent to stamp
// a token for it.
type agentTokenInfo struct {
	ID     string
	Name   string
	Role   string
	Status string
}

// Terminal-vs-transient sentinels: hugen's RemoteStore treats 401/403 as
// FATAL (no retry) and everything else as retryable, so the directory must
// distinguish "this credential is genuinely dead" from "the store hiccuped".
var (
	// errCredentialRejected — the bootstrap secret is unknown, expired, or
	// already consumed. Terminal → 401.
	errCredentialRejected = errors.New("credential rejected")
	// errAgentNotRegistered — no such agent row. Terminal → 403 (a deleted
	// agent holding a signature-valid JWT must not retry forever).
	errAgentNotRegistered = errors.New("agent not registered")
)

// agentTokenDirectory is the narrow store surface the token endpoint
// depends on — a seam so the HTTP handlers are unit-testable without a
// live Hugr. *HubApp implements it over hub.agent.db / hub.db.
// Implementations return the sentinels above for TERMINAL denials; any
// other error is treated as a transient store failure (503).
type agentTokenDirectory interface {
	// agentForToken resolves the agent row (hub.agent.db.agents).
	agentForToken(ctx context.Context, agentID string) (agentTokenInfo, error)
	// redeemBootstrapSecret consumes a one-shot spawn secret by its sha256
	// hex and returns the agent it bootstraps. Fails on unknown, expired,
	// or already-consumed secrets.
	redeemBootstrapSecret(ctx context.Context, secretHash string) (string, error)
}

// agentTokenIssuer signs agent JWTs and serves the /agent/token endpoints.
type agentTokenIssuer struct {
	keyPEM   []byte // private key PEM, handed to qeauth.GenerateToken per issue
	verify   any    // parsed public key for refresh-credential verification
	pubPEM   []byte // PKIX PEM served at /agent/token/public-key
	issuer   string
	tokenTTL time.Duration

	dir    agentTokenDirectory
	logger *slog.Logger
}

func newAgentTokenIssuer(cfg Config, dir agentTokenDirectory, logger *slog.Logger) (*agentTokenIssuer, error) {
	keyPEM, err := os.ReadFile(cfg.AgentJWTKeyFile)
	if err != nil {
		return nil, fmt.Errorf("agent token issuer: read key %s: %w", cfg.AgentJWTKeyFile, err)
	}
	priv, err := qeauth.ParsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("agent token issuer: parse key %s: %w", cfg.AgentJWTKeyFile, err)
	}
	signer, ok := priv.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("agent token issuer: key %s: %T does not expose a public key", cfg.AgentJWTKeyFile, priv)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return nil, fmt.Errorf("agent token issuer: marshal public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	t := &agentTokenIssuer{
		keyPEM:   keyPEM,
		verify:   signer.Public(),
		pubPEM:   pubPEM,
		issuer:   cfg.AgentJWTIssuer,
		tokenTTL: cfg.AgentTokenTTL,
		dir:      dir,
		logger:   logger,
	}
	// Fail at boot, not at first refresh, if the key type can't sign a JWT
	// (qeauth.GenerateToken supports RSA / ECDSA / Ed25519).
	if _, _, err := t.issue(agentTokenInfo{ID: "boot-probe", Role: "agent"}); err != nil {
		return nil, fmt.Errorf("agent token issuer: probe sign: %w", err)
	}
	return t, nil
}

// issue signs a fresh agent JWT (§1.3 claims). Claim names match hugr's
// JwtConfig defaults: sub → user_id, name → user_name, x-hugr-role → role.
func (t *agentTokenIssuer) issue(info agentTokenInfo) (string, int, error) {
	role := info.Role
	if role == "" {
		role = "agent"
	}
	now := time.Now()
	tok, err := qeauth.GenerateToken(t.keyPEM, jwt.MapClaims{
		"iss":         t.issuer,
		"sub":         info.ID,
		"name":        info.Name,
		"x-hugr-role": role,
		"iat":         now.Unix(),
		"exp":         now.Add(t.tokenTTL).Unix(),
	})
	if err != nil {
		return "", 0, err
	}
	return tok, int(t.tokenTTL / time.Second), nil
}

// verifyRefreshCredential checks the signature and issuer of a presented
// agent JWT and returns its subject (= agent_id). Expiry is DELIBERATELY not
// validated — an expired token with a valid signature is the refresh
// credential (§1.2); the status gate and short TTL bound the exposure.
func (t *agentTokenIssuer) verifyRefreshCredential(raw string) (string, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{"RS256", "ES256", "EdDSA"}),
		jwt.WithoutClaimsValidation(),
	)
	claims := jwt.MapClaims{}
	if _, err := parser.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) {
		return t.verify, nil
	}); err != nil {
		return "", fmt.Errorf("verify refresh credential: %w", err)
	}
	if iss, _ := claims["iss"].(string); iss != t.issuer {
		return "", fmt.Errorf("verify refresh credential: issuer %q is not %q", claims["iss"], t.issuer)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", errors.New("verify refresh credential: empty sub")
	}
	return sub, nil
}

// tokenExchangeRequest / tokenExchangeResponse mirror hugen's RemoteStore
// wire contract (pkg/auth/sources/hugr/remote.go) — do not reshape.
type tokenExchangeRequest struct {
	Token string `json:"token"`
}

type tokenExchangeResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error,omitempty"`
}

// handleToken implements POST /agent/token.
func (t *agentTokenIssuer) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req tokenExchangeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Token == "" {
		t.deny(w, http.StatusBadRequest, "invalid request body")
		return
	}

	ctx := r.Context()
	var agentID string
	if looksLikeJWT(req.Token) {
		id, err := t.verifyRefreshCredential(req.Token)
		if err != nil {
			t.logger.Warn("agent token: refresh credential rejected", "err", err)
			t.deny(w, http.StatusUnauthorized, "credential rejected")
			return
		}
		agentID = id
	} else {
		id, err := t.dir.redeemBootstrapSecret(ctx, hashBootstrapSecret(req.Token))
		switch {
		case errors.Is(err, errCredentialRejected):
			t.logger.Warn("agent token: bootstrap secret rejected", "err", err)
			t.deny(w, http.StatusUnauthorized, "credential rejected")
			return
		case err != nil:
			// Transient store failure — 503 so the client's backoff retries;
			// 401 would read as fatal in hugen's RemoteStore.
			t.logger.Error("agent token: bootstrap store failure", "err", err)
			t.deny(w, http.StatusServiceUnavailable, "bootstrap store failure")
			return
		}
		agentID = id
	}

	info, err := t.dir.agentForToken(ctx, agentID)
	switch {
	case errors.Is(err, errAgentNotRegistered):
		// Terminal: a hard-deleted agent with a signature-valid JWT must be
		// told "no" (403 = fatal for the client), not retry on 503 forever.
		t.logger.Warn("agent token: agent not registered", "agent", agentID)
		t.deny(w, http.StatusForbidden, "agent not registered")
		return
	case err != nil:
		// A store failure must not read as "credentials rejected" (fatal in
		// hugen's RemoteStore) — 503 lets the client's backoff retry.
		t.logger.Error("agent token: agent lookup failed", "agent", agentID, "err", err)
		t.deny(w, http.StatusServiceUnavailable, "agent lookup failed")
		return
	}
	// The revocation point (§1.2): a disabled agent's next refresh dies here,
	// within one token TTL of the status flip.
	if info.Status != "" && info.Status != "active" {
		t.logger.Warn("agent token: refresh denied", "agent", agentID, "status", info.Status)
		t.deny(w, http.StatusForbidden, fmt.Sprintf("agent status %q", info.Status))
		return
	}
	// L2 budget gate (§3) plugs in here once L1 usage aggregation lands.

	tok, expiresIn, err := t.issue(info)
	if err != nil {
		t.logger.Error("agent token: sign failed", "agent", agentID, "err", err)
		t.deny(w, http.StatusInternalServerError, "sign failed")
		return
	}
	t.logger.Info("agent token issued", "agent", agentID, "role", info.Role, "expires_in", expiresIn)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(tokenExchangeResponse{AccessToken: tok, ExpiresIn: expiresIn})
}

// handlePublicKey implements GET /agent/token/public-key — the PEM the
// operator registers in hugr's auth config (`jwt: <issuer>: public-key`).
func (t *agentTokenIssuer) handlePublicKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write(t.pubPEM)
}

func (t *agentTokenIssuer) deny(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(tokenExchangeResponse{Error: msg})
}

// mount registers the endpoints on a mux (shared-listener mode; the paths
// are exempted from the hub auth middleware — the body token IS the auth).
func (t *agentTokenIssuer) mount(mux *http.ServeMux) {
	mux.HandleFunc("/agent/token", t.handleToken)
	mux.HandleFunc("/agent/token/public-key", t.handlePublicKey)
}

func looksLikeJWT(s string) bool { return strings.Count(s, ".") == 2 }

func hashBootstrapSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// newBootstrapSecret returns a fresh 32-byte hex secret. Hex has no dots, so
// a bootstrap secret can never be mistaken for a JWT by looksLikeJWT.
func newBootstrapSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate bootstrap secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ---- HubApp: agentTokenDirectory over hub.agent.db / hub.db ----

func (a *HubApp) agentForToken(ctx context.Context, agentID string) (agentTokenInfo, error) {
	res, err := a.client.Query(ctx,
		`query($aid: String!) { hub { agent { db { agents(
			filter: { id: { eq: $aid } } limit: 1
		) { id name status role } } } } }`,
		map[string]any{"aid": agentID},
	)
	if err != nil {
		return agentTokenInfo{}, fmt.Errorf("agent lookup %q: %w", agentID, err)
	}
	defer res.Close()
	if res.Err() != nil {
		return agentTokenInfo{}, fmt.Errorf("agent lookup %q: %w", agentID, res.Err())
	}
	var agents []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Role   string `json:"role"`
	}
	if err := res.ScanData("hub.agent.db.agents", &agents); err != nil {
		// The hugr client reports an empty filtered result as ErrNoData — that
		// is "no such agent" (terminal), not a scan failure. The token path
		// needs it as errAgentNotRegistered (→ 403 for a deleted agent), and
		// create_agent reads it as "the id is free".
		if errors.Is(err, types.ErrNoData) {
			return agentTokenInfo{}, fmt.Errorf("agent %q: %w", agentID, errAgentNotRegistered)
		}
		return agentTokenInfo{}, fmt.Errorf("agent lookup %q: scan: %w", agentID, err)
	}
	if len(agents) == 0 {
		return agentTokenInfo{}, fmt.Errorf("agent %q: %w", agentID, errAgentNotRegistered)
	}
	ag := agents[0]
	return agentTokenInfo{ID: ag.ID, Name: ag.Name, Role: ag.Role, Status: ag.Status}, nil
}

func (a *HubApp) redeemBootstrapSecret(ctx context.Context, secretHash string) (string, error) {
	res, err := a.client.Query(ctx,
		`query($h: String!) { hub { db { agent_bootstrap_secrets(
			filter: { secret_hash: { eq: $h } } limit: 1
		) { id agent_id expires_at consumed_at } } } }`,
		map[string]any{"h": secretHash},
	)
	if err != nil {
		return "", fmt.Errorf("bootstrap lookup: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return "", fmt.Errorf("bootstrap lookup: %w", res.Err())
	}
	var rows []struct {
		ID         string     `json:"id"`
		AgentID    string     `json:"agent_id"`
		ExpiresAt  time.Time  `json:"expires_at"`
		ConsumedAt *time.Time `json:"consumed_at"`
	}
	if err := res.ScanData("hub.db.agent_bootstrap_secrets", &rows); err != nil {
		// The hugr client reports an empty filtered result as ErrNoData —
		// that is "unknown secret" (terminal), not a store failure.
		if errors.Is(err, types.ErrNoData) {
			return "", fmt.Errorf("bootstrap secret unknown: %w", errCredentialRejected)
		}
		return "", fmt.Errorf("bootstrap lookup: scan: %w", err)
	}
	if len(rows) == 0 {
		return "", fmt.Errorf("bootstrap secret unknown: %w", errCredentialRejected)
	}
	row := rows[0]
	if row.ConsumedAt != nil {
		return "", fmt.Errorf("bootstrap secret already consumed: %w", errCredentialRejected)
	}
	if time.Now().After(row.ExpiresAt) {
		return "", fmt.Errorf("bootstrap secret expired: %w", errCredentialRejected)
	}

	// One-shot claim: the consumed_at IS NULL filter guarantees only the
	// first concurrent update lands. NOTE: updates take the `_mut_data`
	// input type; `_mut_input_data` is insert-only.
	//
	// The claim is confirmed by a UNIQUE NONCE written over secret_hash,
	// NOT by affected_rows and NOT by a timestamp: hugr's postgres source
	// reports affected_rows=0 even for a successful update, and Timestamp
	// round-trips at second precision (both found at the live gate,
	// 2026-07-06 — query-engine issues). Overwriting the hash also erases
	// the redeemable credential from the row entirely.
	nonce, err := newBootstrapSecret()
	if err != nil {
		return "", err
	}
	claim := "consumed:" + nonce
	upd, err := a.client.Query(ctx,
		`mutation($id: String!, $data: hub_db_agent_bootstrap_secrets_mut_data!) {
			hub { db { update_agent_bootstrap_secrets(
				filter: { id: { eq: $id }, consumed_at: { is_null: true } }
				data: $data
			) { affected_rows } } } }`,
		map[string]any{
			"id": row.ID,
			"data": map[string]any{
				"consumed_at": time.Now().UTC().Format(time.RFC3339Nano),
				"secret_hash": claim,
			},
		},
	)
	if err != nil {
		return "", fmt.Errorf("bootstrap consume: %w", err)
	}
	defer upd.Close()
	if upd.Err() != nil {
		return "", fmt.Errorf("bootstrap consume: %w", upd.Err())
	}

	chk, err := a.client.Query(ctx,
		`query($id: String!) { hub { db { agent_bootstrap_secrets(
			filter: { id: { eq: $id } } limit: 1
		) { secret_hash } } } }`,
		map[string]any{"id": row.ID},
	)
	if err != nil {
		return "", fmt.Errorf("bootstrap confirm: %w", err)
	}
	defer chk.Close()
	if chk.Err() != nil {
		return "", fmt.Errorf("bootstrap confirm: %w", chk.Err())
	}
	var after []struct {
		SecretHash string `json:"secret_hash"`
	}
	if err := chk.ScanData("hub.db.agent_bootstrap_secrets", &after); err != nil {
		return "", fmt.Errorf("bootstrap confirm: scan: %w", err)
	}
	if len(after) == 0 || after[0].SecretHash != claim {
		return "", fmt.Errorf("bootstrap secret already consumed: %w", errCredentialRejected)
	}
	return row.AgentID, nil
}

// ---- bootstrap mint (admin) — `mutation { function { hub { agent { bootstrap_token } } } }` ----

func agentBootstrapType() app.Type {
	return app.Struct("agent_bootstrap").
		Desc("One-shot spawn credential for an agent container. `secret` is returned ONCE — only its hash is stored.").
		Field("agent_id", app.String).
		Field("secret", app.String).
		Field("expires_at", app.String).
		AsType()
}

// registerAgentBootstrap wires the mint mutation into the `agent` module,
// next to agent_info. Admin-gated: containers never mint their own secrets —
// the spawner does (HB4) and hands the plaintext to the container env.
func (a *HubApp) registerAgentBootstrap() error {
	return a.mux.HandleFunc("agent", "bootstrap_token", a.handleMintBootstrap,
		app.Arg("agent_id", app.String),
		app.ArgFromContext("user_id", app.String, app.AuthUserID),
		app.ArgFromContext("user_name", app.String, app.AuthUserName),
		app.ArgFromContext("role", app.String, app.AuthRole),
		app.ArgFromContext("auth_type", app.String, app.AuthType),
		app.Return(agentBootstrapType()),
		app.Mutation(),
		app.Desc("Mint a one-shot bootstrap secret for an agent container spawn (spec-hub-side §1.5). The plaintext is returned once; /agent/token redeems it for the agent's first JWT. Admin only."),
	)
}

func (a *HubApp) handleMintBootstrap(w *app.Result, r *app.Request) error {
	// A secret minted while the issuer is disabled could never be redeemed
	// (the /agent/token endpoint is not mounted) — fail loudly at mint time.
	if a.config.AgentJWTKeyFile == "" {
		return errors.New("agent token issuer disabled (HUB_AGENT_JWT_KEY not set)")
	}
	u := userFromArgs(r)
	if err := requireUser(u); err != nil {
		return err
	}
	ctx := withIdentity(r.Context(), u)
	if err := a.requireAdmin(ctx, u); err != nil {
		return err
	}
	agentID := r.String("agent_id")
	if agentID == "" {
		return errors.New("agent_id is required")
	}
	// The agent must exist — a secret for a ghost agent would 503 forever
	// at redeem time; fail loudly at mint time instead.
	if _, err := a.agentForToken(ctx, agentID); err != nil {
		return err
	}

	secret, expiresAt, err := a.mintBootstrapForAgent(ctx, agentID)
	if err != nil {
		return err
	}

	return w.SetJSON(map[string]any{
		"agent_id":   agentID,
		"secret":     secret,
		"expires_at": expiresAt.Format(time.RFC3339Nano),
	})
}

// mintBootstrapForAgent generates a one-shot spawn secret for agentID, stores
// only its sha256 hash in the platform DB, and returns the plaintext plus its
// expiry. The caller MUST have already verified admin authority and that the
// agent exists (a secret for a ghost agent 503s forever at redeem time).
//
// It requires the token issuer (HUB_AGENT_JWT_KEY): a secret minted while the
// issuer is disabled could never be redeemed, since /agent/token is not
// mounted — so it fails loudly rather than handing back a dead credential.
// Shared by the standalone bootstrap_token mint and create_agent's
// provision-and-boot path.
func (a *HubApp) mintBootstrapForAgent(ctx context.Context, agentID string) (string, time.Time, error) {
	if a.config.AgentJWTKeyFile == "" {
		return "", time.Time{}, errors.New("agent token issuer disabled (HUB_AGENT_JWT_KEY not set)")
	}
	secret, err := newBootstrapSecret()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := time.Now().UTC().Add(a.config.AgentBootstrapTTL)
	res, err := a.client.Query(ctx,
		`mutation($data: hub_db_agent_bootstrap_secrets_mut_input_data!) {
			hub { db { insert_agent_bootstrap_secrets(data: $data) { id } } } }`,
		map[string]any{"data": map[string]any{
			"id":          newBootstrapID(),
			"agent_id":    agentID,
			"secret_hash": hashBootstrapSecret(secret),
			"expires_at":  expiresAt.Format(time.RFC3339Nano),
		}},
	)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("store bootstrap secret: %w", err)
	}
	defer res.Close()
	if res.Err() != nil {
		return "", time.Time{}, fmt.Errorf("store bootstrap secret: %w", res.Err())
	}
	return secret, expiresAt, nil
}

func newBootstrapID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "abs-" + hex.EncodeToString(b)
}
