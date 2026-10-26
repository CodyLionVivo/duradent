-- Pricing: price lists per customer group, tiered unit prices per variant.
-- variant_id has no foreign key on purpose: pricing does not depend on the
-- catalog schema, only on variant ids.

CREATE TABLE price_lists (
    id         BINARY(16)  NOT NULL,
    code       VARCHAR(32) NOT NULL,
    name       VARCHAR(80) NOT NULL,
    is_default BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at DATETIME(6) NOT NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_price_lists_code (code)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE prices (
    price_list_id BINARY(16)    NOT NULL,
    variant_id    BINARY(16)    NOT NULL,
    currency      CHAR(3)       NOT NULL,
    min_qty       INT UNSIGNED  NOT NULL DEFAULT 1,
    amount        DECIMAL(12, 2) NOT NULL,
    PRIMARY KEY (price_list_id, variant_id, currency, min_qty),
    KEY idx_prices_variant (variant_id),
    CONSTRAINT fk_prices_list FOREIGN KEY (price_list_id) REFERENCES price_lists (id) ON DELETE CASCADE,
    CONSTRAINT chk_prices_amount CHECK (amount > 0)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
