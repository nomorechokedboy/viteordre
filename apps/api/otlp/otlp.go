package otlp

import (
	"context"

	"encore.app/internal/logger"
	"encore.app/internal/otel"
)

//encore:service
type Service struct {
	config otel.OtelConfig
}

// initService is automatically called by Encore when the app starts
// This is the recommended way to initialize services in Encore
func initService() (*Service, error) {
	ctx := context.Background()

	cfg, err := otel.New(ctx)
	if err != nil {
		return nil, err
	}

	return &Service{config: cfg}, nil
}

func (s *Service) Shutdown(ctx context.Context) {
	if err := s.config.Shutdown(ctx); err != nil {
		logger.ErrorContext(ctx, "Service.Shutdown Otel err: ", err)
	}
}

// Health check endpoint to verify OTEL is initialized
//
//encore:api public method=GET path=/otel/health
func (s *Service) Health(ctx context.Context) (*HealthResponse, error) {
	return &HealthResponse{
		Status:          "ok",
		OTELInitialized: s.config != nil,
	}, nil
}

type HealthResponse struct {
	Status          string `json:"status"`
	OTELInitialized bool   `json:"otel_initialized"`
}
