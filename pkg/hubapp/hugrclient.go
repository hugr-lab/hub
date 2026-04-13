package hubapp

import (
	"time"

	"github.com/hugr-lab/query-engine/client"
)

// NewHugrClient creates a Hugr client with native secret-key auth and subscription pool.
// subPoolMax controls the maximum WebSocket connections for subscriptions (default 20).
func NewHugrClient(hugrURL, secretKey string, timeout time.Duration, subPoolMax int) *client.Client {
	if subPoolMax <= 0 {
		subPoolMax = 20
	}
	idle := subPoolMax / 4
	if idle < 1 {
		idle = 1
	}
	return client.NewClient(hugrURL,
		client.WithSecretKeyAuth(secretKey),
		client.WithTimeout(timeout),
		client.WithSubscriptionPool(subPoolMax, idle),
	)
}
