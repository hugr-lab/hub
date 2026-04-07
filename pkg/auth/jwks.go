package auth

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

// JWKSProvider fetches and caches OIDC public keys for JWT validation.
type JWKSProvider struct {
	hugrURL string
	mu      sync.RWMutex
	key     *rsa.PublicKey
	fetched time.Time
	ttl     time.Duration
}

func NewJWKSProvider(hugrURL string) *JWKSProvider {
	return &JWKSProvider{
		hugrURL: hugrURL,
		ttl:     1 * time.Hour,
	}
}

// PublicKey returns the cached RSA public key, fetching if needed.
func (p *JWKSProvider) PublicKey() (*rsa.PublicKey, error) {
	p.mu.RLock()
	if p.key != nil && time.Since(p.fetched) < p.ttl {
		key := p.key
		p.mu.RUnlock()
		return key, nil
	}
	p.mu.RUnlock()

	return p.refresh()
}

// Refresh forces a key re-fetch (e.g., on validation failure).
func (p *JWKSProvider) Refresh() (*rsa.PublicKey, error) {
	return p.refresh()
}

func (p *JWKSProvider) refresh() (*rsa.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 1. Get OIDC issuer from Hugr auth config
	issuer, err := p.fetchIssuer()
	if err != nil {
		return nil, fmt.Errorf("fetch issuer: %w", err)
	}

	// 2. Fetch OIDC well-known config
	jwksURI, err := p.fetchJWKSURI(issuer)
	if err != nil {
		return nil, fmt.Errorf("fetch jwks_uri: %w", err)
	}

	// 3. Fetch JWKS and extract RSA key
	key, err := p.fetchRSAKey(jwksURI)
	if err != nil {
		return nil, fmt.Errorf("fetch RSA key: %w", err)
	}

	p.key = key
	p.fetched = time.Now()
	return key, nil
}

func (p *JWKSProvider) fetchIssuer() (string, error) {
	resp, err := http.Get(p.hugrURL + "/auth/config")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("auth/config: %d", resp.StatusCode)
	}
	var cfg struct {
		Issuer string `json:"issuer"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return "", err
	}
	if cfg.Issuer == "" {
		return "", fmt.Errorf("no issuer in auth config")
	}
	return cfg.Issuer, nil
}

func (p *JWKSProvider) fetchJWKSURI(issuer string) (string, error) {
	resp, err := http.Get(issuer + "/.well-known/openid-configuration")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("openid-configuration: %d", resp.StatusCode)
	}
	var cfg struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cfg); err != nil {
		return "", err
	}
	if cfg.JWKSURI == "" {
		return "", fmt.Errorf("no jwks_uri in OIDC config")
	}
	return cfg.JWKSURI, nil
}

func (p *JWKSProvider) fetchRSAKey(jwksURI string) (*rsa.PublicKey, error) {
	resp, err := http.Get(jwksURI)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("jwks: %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, err
	}

	for _, k := range jwks.Keys {
		if k.Kty == "RSA" && (k.Use == "sig" || k.Use == "") {
			return parseRSAKey(k.N, k.E)
		}
	}
	return nil, fmt.Errorf("no RSA signing key in JWKS")
}

func parseRSAKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, fmt.Errorf("decode N: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, fmt.Errorf("decode E: %w", err)
	}
	n := new(big.Int).SetBytes(nBytes)
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
}
