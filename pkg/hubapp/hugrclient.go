package hubapp

import (
	"time"

	"github.com/hugr-lab/query-engine/client"
)

// NewHugrClient creates a Hugr client with native secret-key auth and subscription pool.
func NewHugrClient(hugrURL, secretKey string, timeout time.Duration) *client.Client {
	return client.NewClient(hugrURL,
		client.WithSecretKeyAuth(secretKey),
		client.WithTimeout(timeout),
		client.WithSubscriptionPool(5, 2),
	)
}
