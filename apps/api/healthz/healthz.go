package healthz

import (
	"context"

	"encore.app/internal/healthcheck"
)

// Healthcheck endpoint
//
//encore:api public method=GET path=/healthz
func HealthCheck(ctx context.Context) (*healthcheck.HealthCheckResponse, error) {
	return healthcheck.GetHealthCheckResponse(), nil
}
