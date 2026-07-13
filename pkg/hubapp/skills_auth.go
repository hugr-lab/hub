package hubapp

// Marketplace caller verification (SK1). The /skills/* surface is exempt from
// the OIDC-JWKS auth middleware (pkg/auth/middleware.go) because it must
// accept BOTH end-user IdP tokens AND hub-minted agent tokens. It resolves the
// caller by asking hugr itself — auth.me, the authority hugr trusts for both
// kinds — via a per-token query-engine client, exactly as the hugen container
// does (cmd/hugen/serve_verify.go). Results are cached briefly so a reconciler
// burst resolves to one round-trip.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	hubident "github.com/hugr-lab/hugen/pkg/identity/hub"
	"github.com/hugr-lab/query-engine/client"
)

// skillsCaller is a verified marketplace caller. ID is the user_id for an
// end-user token or the agent_id for an agent token; Role is the hugr role the
// §4 gates evaluate.
type skillsCaller struct {
	ID   string
	Name string
	Role string
}

// verifySkillsCaller resolves bearer against hugr auth.me. An empty or
// hugr-rejected token yields an error → the handler answers 401.
func (a *HubApp) verifySkillsCaller(ctx context.Context, bearer string) (skillsCaller, error) {
	bearer = strings.TrimSpace(bearer)
	if bearer == "" {
		return skillsCaller{}, errors.New("skills: no bearer token")
	}
	if a.skillsVerify != nil {
		if c, ok := a.skillsVerify.get(bearer); ok {
			return c, nil
		}
	}
	// a.config.HugrURL already carries the /ipc suffix the app framework needs.
	qe := client.NewClient(a.config.HugrURL,
		client.WithTransport(bearerRT{token: bearer, base: http.DefaultTransport}))
	who, err := hubident.New(qe).WhoAmI(ctx)
	if err != nil {
		return skillsCaller{}, err
	}
	c := skillsCaller{ID: who.UserID, Name: who.UserName, Role: who.Role}
	if a.skillsVerify != nil {
		a.skillsVerify.put(bearer, c)
	}
	return c, nil
}

// bearerRT injects a static Authorization: Bearer <token> so the per-caller
// hugr client authenticates as the caller (contrast a.client, which is the
// hub's own secret-key principal).
type bearerRT struct {
	token string
	base  http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

// verifyCache is a small TTL cache keyed by a hash of the token so a request
// burst from one caller collapses to one auth.me round-trip. The raw token is
// never used as a map key.
type verifyCache struct {
	ttl time.Duration
	mu  sync.Mutex
	m   map[string]verifyEntry
}

type verifyEntry struct {
	caller  skillsCaller
	expires time.Time
}

func newVerifyCache(ttl time.Duration) *verifyCache {
	return &verifyCache{ttl: ttl, m: map[string]verifyEntry{}}
}

func (c *verifyCache) key(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

func (c *verifyCache) get(tok string) (skillsCaller, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[c.key(tok)]
	if !ok || time.Now().After(e.expires) {
		return skillsCaller{}, false
	}
	return e.caller, true
}

func (c *verifyCache) put(tok string, caller skillsCaller) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[c.key(tok)] = verifyEntry{caller: caller, expires: time.Now().Add(c.ttl)}
}
