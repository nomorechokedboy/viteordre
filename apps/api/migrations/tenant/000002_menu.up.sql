-- Menu catalog is merchant-wide. Each branch opts in per item and may override price
-- and availability. Items are never hard-deleted, set is_active = 0 instead, because
-- order history references them.

CREATE TABLE menu_categories (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active  INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE menu_items (
    id          TEXT PRIMARY KEY,
    category_id TEXT NOT NULL REFERENCES menu_categories (id),
    name        TEXT NOT NULL,
    description TEXT,
    image_url   TEXT,
    base_price  INTEGER NOT NULL CHECK (base_price >= 0),
    sort_order  INTEGER NOT NULL DEFAULT 0,
    is_active   INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX idx_items_category ON menu_items (category_id, sort_order);

-- Option groups such as Size or Toppings, owned by one item.
CREATE TABLE item_option_groups (
    id         TEXT PRIMARY KEY,
    item_id    TEXT NOT NULL REFERENCES menu_items (id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    min_select INTEGER NOT NULL DEFAULT 0 CHECK (min_select >= 0),
    max_select INTEGER NOT NULL DEFAULT 1 CHECK (max_select >= min_select),
    sort_order INTEGER NOT NULL DEFAULT 0
) STRICT;
CREATE INDEX idx_option_groups_item ON item_option_groups (item_id, sort_order);

CREATE TABLE item_options (
    id          TEXT PRIMARY KEY,
    group_id    TEXT NOT NULL REFERENCES item_option_groups (id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    price_delta INTEGER NOT NULL DEFAULT 0,
    sort_order  INTEGER NOT NULL DEFAULT 0,
    is_active   INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1))
) STRICT;
CREATE INDEX idx_options_group ON item_options (group_id, sort_order);

-- A row means this branch sells this item. price NULL means use the item base_price.
CREATE TABLE branch_menu_items (
    branch_id    TEXT NOT NULL REFERENCES branches (id) ON DELETE CASCADE,
    item_id      TEXT NOT NULL REFERENCES menu_items (id) ON DELETE CASCADE,
    price        INTEGER CHECK (price IS NULL OR price >= 0),
    is_available INTEGER NOT NULL DEFAULT 1 CHECK (is_available IN (0, 1)),
    sort_order   INTEGER,
    PRIMARY KEY (branch_id, item_id)
) STRICT;
CREATE INDEX idx_branch_items_item ON branch_menu_items (item_id);

-- Sale periods, evaluated in the branch timezone.
-- Semantics: an item with NO active windows is sellable at any time. An item with one or
-- more active windows is sellable only while at least one of them matches.
--   branch_id      : NULL applies the window to every branch
--   days_of_week   : bitmask, bit 0 is Monday up to bit 6 Sunday, 127 is every day
--   start/end_minute : minutes since local midnight. end_minute below start_minute means
--                      the window crosses midnight. A full day is 0 to 1439.
--   valid_from/to  : local dates as YYYY-MM-DD, NULL is open ended
--   price_override : NULL is an availability window only, a value is a happy-hour price
CREATE TABLE item_sale_windows (
    id             TEXT PRIMARY KEY,
    item_id        TEXT NOT NULL REFERENCES menu_items (id) ON DELETE CASCADE,
    branch_id      TEXT REFERENCES branches (id) ON DELETE CASCADE,
    days_of_week   INTEGER NOT NULL DEFAULT 127 CHECK (days_of_week BETWEEN 1 AND 127),
    start_minute   INTEGER NOT NULL DEFAULT 0 CHECK (start_minute BETWEEN 0 AND 1439),
    end_minute     INTEGER NOT NULL DEFAULT 1439 CHECK (end_minute BETWEEN 0 AND 1439),
    valid_from     TEXT,
    valid_to       TEXT,
    price_override INTEGER CHECK (price_override IS NULL OR price_override >= 0),
    is_active      INTEGER NOT NULL DEFAULT 1 CHECK (is_active IN (0, 1)),
    created_at     INTEGER NOT NULL,
    CHECK (start_minute <> end_minute)
) STRICT;
CREATE INDEX idx_sale_windows_item ON item_sale_windows (item_id, is_active);
CREATE INDEX idx_sale_windows_branch ON item_sale_windows (branch_id);
