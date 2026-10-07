package config

import "log/slog"

var _ slog.LogValuer = (*OtelConfig)(nil)

type OtelConfig struct {
	Endpoint      string `env:"OTEL_ENDPOINT" env-default:"localhost:4318"                          json:"otel_endpoint"`
	SchemaVersion string `env:"OTEL_VERSION"  env-default:"1.26.0"                                  json:"otel_schema_version"`
	SchemaUrl     string `env:"OTEL_URL"      env-default:"https://opentelemetry.io/schemas/1.37.0" json:"otel_schema_url"`
}

func (c *OtelConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("endpoint", generateMaskedString(c.Endpoint)),
		slog.String("schema_version", generateMaskedString(c.SchemaVersion)),
	)
}
