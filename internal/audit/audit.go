package audit

import (
	"context"
	"database/sql"
)

// Log writes a row to audit_log. Failures are swallowed to avoid breaking the caller.
func Log(ctx context.Context, db *sql.DB, actor, action, detail string) {
	if db == nil {
		return
	}
	_, _ = db.ExecContext(ctx, `INSERT INTO audit_log(actor, action, detail) VALUES(?,?,?)`, actor, action, detail)
}
