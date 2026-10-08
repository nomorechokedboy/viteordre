-- Floor plan, QR tables, table sessions and orders.
-- Customers are anonymous. An order belongs to a table session, never to a user account.
-- The QR code encodes /o/{merchant_slug}/{qr_token}. The token is the only public handle
-- to a table, so it is random and can be rotated to invalidate printed codes.

CREATE TABLE table_areas (
    id         TEXT PRIMARY KEY,
    branch_id  TEXT NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX idx_areas_branch ON table_areas (branch_id, sort_order);

-- Tables or seats. Never hard-deleted once used, set is_active = 0 instead.
CREATE TABLE dining_tables (
    id         TEXT PRIMARY KEY,
    branch_id  TEXT NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    area_id    TEXT REFERENCES table_areas (id) ON DELETE SET NULL,
    code       TEXT NOT NULL,
    seats      INTEGER NOT NULL DEFAULT 2 CHECK (seats > 0),
    qr_token   TEXT NOT NULL UNIQUE,
    is_active  INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE (branch_id, code),
    UNIQUE (id, branch_id)
) STRICT;
CREATE INDEX idx_tables_branch ON dining_tables (branch_id, is_active);

-- One visit at a table: several order rounds, one bill.
-- open_table_id is the guard for at most one open session per table. It equals table_id
-- while the session is open and is NULL once closed, and NULLs are distinct in UNIQUE.
CREATE TABLE table_sessions (
    id            TEXT PRIMARY KEY,
    branch_id     TEXT NOT NULL,
    table_id      TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'closed')),
    open_table_id TEXT UNIQUE,
    guest_count   INTEGER CHECK (guest_count IS NULL OR guest_count > 0),
    opened_at     INTEGER NOT NULL,
    closed_at     INTEGER,
    UNIQUE (id, table_id, branch_id),
    FOREIGN KEY (table_id, branch_id) REFERENCES dining_tables (id, branch_id),
    CHECK (
        (status = 'open' AND open_table_id = table_id AND closed_at IS NULL)
        OR (status = 'closed' AND open_table_id IS NULL AND closed_at IS NOT NULL)
    )
) STRICT;
CREATE INDEX idx_sessions_branch ON table_sessions (branch_id, status, opened_at);
CREATE INDEX idx_sessions_table ON table_sessions (table_id, opened_at);

-- Human-friendly order numbers, per branch per local business day. Increment last_no and
-- read it back inside the same transaction that inserts the order.
CREATE TABLE order_counters (
    branch_id  TEXT NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    order_date TEXT NOT NULL,
    last_no    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (branch_id, order_date)
) STRICT;

-- Amounts are computed by the app, all in minor units of currency.
--   total = subtotal - discount_total + service_total, plus tax_total when prices exclude tax
CREATE TABLE orders (
    id             TEXT PRIMARY KEY,
    branch_id      TEXT NOT NULL,
    table_id       TEXT NOT NULL,
    session_id     TEXT NOT NULL,
    order_date     TEXT NOT NULL,
    order_no       INTEGER NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'accepted', 'preparing', 'served', 'completed', 'cancelled')),
    currency       TEXT NOT NULL,
    subtotal       INTEGER NOT NULL DEFAULT 0 CHECK (subtotal >= 0),
    discount_total INTEGER NOT NULL DEFAULT 0 CHECK (discount_total >= 0),
    service_total  INTEGER NOT NULL DEFAULT 0 CHECK (service_total >= 0),
    tax_total      INTEGER NOT NULL DEFAULT 0 CHECK (tax_total >= 0),
    total          INTEGER NOT NULL DEFAULT 0 CHECK (total >= 0),
    guest_name     TEXT,
    note           TEXT,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    UNIQUE (branch_id, order_date, order_no),
    FOREIGN KEY (session_id, table_id, branch_id) REFERENCES table_sessions (id, table_id, branch_id)
) STRICT;
CREATE INDEX idx_orders_branch_status ON orders (branch_id, status, created_at);
CREATE INDEX idx_orders_session ON orders (session_id);

-- Name and prices are snapshots taken at order time, so editing the menu later never
-- rewrites history. unit_price already reflects any branch price or happy-hour override.
CREATE TABLE order_items (
    id            TEXT PRIMARY KEY,
    order_id      TEXT NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    menu_item_id  TEXT NOT NULL REFERENCES menu_items (id),
    name_snapshot TEXT NOT NULL,
    unit_price    INTEGER NOT NULL CHECK (unit_price >= 0),
    options_total INTEGER NOT NULL DEFAULT 0,
    qty           INTEGER NOT NULL CHECK (qty > 0),
    line_total    INTEGER NOT NULL CHECK (line_total >= 0),
    note          TEXT,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending', 'preparing', 'served', 'cancelled')),
    created_at    INTEGER NOT NULL,
    CHECK (line_total = (unit_price + options_total) * qty)
) STRICT;
CREATE INDEX idx_order_items_order ON order_items (order_id);
CREATE INDEX idx_order_items_menu_item ON order_items (menu_item_id);

CREATE TABLE order_item_options (
    id            TEXT PRIMARY KEY,
    order_item_id TEXT NOT NULL REFERENCES order_items (id) ON DELETE CASCADE,
    option_id     TEXT REFERENCES item_options (id) ON DELETE SET NULL,
    name_snapshot TEXT NOT NULL,
    price_delta   INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX idx_order_item_options_item ON order_item_options (order_item_id);
