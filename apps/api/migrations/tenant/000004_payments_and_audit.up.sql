-- Payments, provider webhook log and audit trail.
--
-- A payment settles one or more orders of a table session (payment_orders). Two ways to
-- complete one:
--   automatic : a SePay webhook matches reference_code and amount, provider and
--               provider_txn_id are set, status becomes paid
--   manual    : staff confirm, confirmed_by_user_id is set, status becomes paid
-- The app must also ensure an order is not part of two live payments at once.

CREATE TABLE payments (
    id                   TEXT PRIMARY KEY,
    session_id           TEXT NOT NULL REFERENCES table_sessions (id),
    bank_account_id      TEXT REFERENCES bank_accounts (id) ON DELETE SET NULL,
    method               TEXT NOT NULL CHECK (method IN ('bank_qr', 'cash')),
    provider             TEXT CHECK (provider IS NULL OR provider IN ('sepay')),
    status               TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'paid', 'needs_review', 'expired', 'failed', 'cancelled')),
    amount               INTEGER NOT NULL CHECK (amount > 0),
    currency             TEXT NOT NULL,
    reference_code       TEXT NOT NULL UNIQUE,
    qr_payload           TEXT,
    provider_txn_id      TEXT UNIQUE,
    paid_amount          INTEGER CHECK (paid_amount IS NULL OR paid_amount >= 0),
    paid_at              INTEGER,
    expires_at           INTEGER,
    confirmed_by_user_id TEXT,
    confirm_note         TEXT,
    created_at           INTEGER NOT NULL,
    updated_at           INTEGER NOT NULL,
    CHECK (status <> 'paid' OR paid_at IS NOT NULL)
) STRICT;
CREATE INDEX idx_payments_session ON payments (session_id);
CREATE INDEX idx_payments_status ON payments (status, created_at);

CREATE TABLE payment_orders (
    payment_id TEXT NOT NULL REFERENCES payments (id) ON DELETE CASCADE,
    order_id   TEXT NOT NULL REFERENCES orders (id),
    PRIMARY KEY (payment_id, order_id)
) STRICT;
CREATE INDEX idx_payment_orders_order ON payment_orders (order_id);

-- Every raw webhook delivery. UNIQUE (provider, provider_event_id) makes retries idempotent.
CREATE TABLE payment_events (
    id                TEXT PRIMARY KEY,
    provider          TEXT NOT NULL CHECK (provider IN ('sepay')),
    provider_event_id TEXT NOT NULL,
    payment_id        TEXT REFERENCES payments (id) ON DELETE SET NULL,
    payload           TEXT NOT NULL,
    outcome           TEXT NOT NULL DEFAULT 'received'
                      CHECK (outcome IN ('received', 'matched', 'unmatched', 'needs_review', 'duplicate', 'rejected')),
    received_at       INTEGER NOT NULL,
    processed_at      INTEGER,
    UNIQUE (provider, provider_event_id)
) STRICT;
CREATE INDEX idx_payment_events_payment ON payment_events (payment_id);
CREATE INDEX idx_payment_events_received ON payment_events (received_at);

-- Append-only audit trail. Retention is a purge by created_at, see idx_audit_created.
CREATE TABLE audit_logs (
    id            TEXT PRIMARY KEY,
    actor_type    TEXT NOT NULL CHECK (actor_type IN ('user', 'customer', 'system', 'webhook')),
    actor_user_id TEXT,
    action        TEXT NOT NULL,
    entity_type   TEXT NOT NULL,
    entity_id     TEXT,
    diff          TEXT,
    ip            TEXT,
    created_at    INTEGER NOT NULL
) STRICT;
CREATE INDEX idx_audit_created ON audit_logs (created_at);
CREATE INDEX idx_audit_entity ON audit_logs (entity_type, entity_id, created_at);
CREATE INDEX idx_audit_actor ON audit_logs (actor_user_id, created_at);
