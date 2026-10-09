// Package migrations embeds the SQL migration files so the dbmigrate CLI is a single
// self-contained binary. The root of FS holds one directory per target: control and tenant.
package migrations

import "embed"

// FS contains control/*.sql and tenant/*.sql.
//
//go:embed control/*.sql tenant/*.sql
var FS embed.FS
