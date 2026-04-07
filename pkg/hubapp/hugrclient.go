package hubapp

import (
	"time"

	"github.com/hugr-lab/hub/pkg/auth"
	"github.com/hugr-lab/query-engine/client"
)

// NewHugrClient creates a Hugr client with management auth + user identity from context.
func NewHugrClient(hugrURL, secretKey string, timeout time.Duration) *client.Client {
	return client.NewClient(hugrURL,
		client.WithTransport(&auth.UserTransport{}),
		client.WithApiKeyCustomHeader(secretKey, "x-hugr-secret-key"),
		client.WithTimeout(timeout),
	)
}
