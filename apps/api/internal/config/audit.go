package config

import "log/slog"

// AuditConfig holds settings for the audit log subsystem.
type AuditConfig struct {
	// PurgeTime is the daily wall-clock time at which old audit logs are
	// purged. Format: "HH:MM" (24-hour, UTC). Default: "02:00".
	PurgeTime string `env:"AUDIT_PURGE_TIME" env-default:"02:00"`

	// RetentionDays is how many days of audit logs to keep. Default: 90.
	RetentionDays int `env:"AUDIT_RETENTION_DAYS" env-default:"90"`
}

var _ slog.LogValuer = (*AuditConfig)(nil)

func (c *AuditConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("AUDIT_PURGE_TIME", c.PurgeTime),
		slog.Int("AUDIT_RETENTION_DAYS", c.RetentionDays),
	)
}
