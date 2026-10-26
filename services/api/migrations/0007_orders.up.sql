-- Orders: carts, orders with a frozen price snapshot, their status history,
-- and every payment attempt. Amounts exclude nothing: totals include IGV.

CREATE TABLE carts (
    id         BINARY(16)  NOT NULL,
    account_id BINARY(16)  NULL,
    cart_key   VARCHAR(64) NULL,
    updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_carts_account (account_id),
    UNIQUE KEY uk_carts_key (cart_key),
    KEY idx_carts_updated (updated_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE cart_items (
    cart_id    BINARY(16) NOT NULL,
    variant_id BINARY(16) NOT NULL,
    qty        INT        NOT NULL,
    position   INT        NOT NULL DEFAULT 0,
    PRIMARY KEY (cart_id, variant_id),
    CONSTRAINT fk_cart_items_cart FOREIGN KEY (cart_id) REFERENCES carts (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- Human-friendly order numbers (DD-000001) from an auto-increment sequence.
CREATE TABLE order_numbers (
    n BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    PRIMARY KEY (n)
) ENGINE = InnoDB;

CREATE TABLE orders (
    id               BINARY(16)                                                                   NOT NULL,
    number           VARCHAR(20)                                                                  NOT NULL,
    account_id       BINARY(16)                                                                   NOT NULL,
    email            VARCHAR(254)                                                                 NOT NULL,
    status           ENUM ('pending_payment', 'paid', 'preparing', 'shipped', 'delivered', 'cancelled') NOT NULL,
    payment_method   ENUM ('culqi', 'bank_transfer')                                              NOT NULL,
    currency         CHAR(3)                                                                      NOT NULL,
    subtotal         DECIMAL(12, 2)                                                               NOT NULL,
    shipping_amount  DECIMAL(12, 2)                                                               NOT NULL,
    tax_amount       DECIMAL(12, 2)                                                               NOT NULL,
    total            DECIMAL(12, 2)                                                               NOT NULL,
    billing          JSON                                                                         NOT NULL,
    shipping         JSON                                                                         NOT NULL,
    notes            VARCHAR(500)                                                                 NOT NULL DEFAULT '',
    payment_deadline DATETIME(6)                                                                  NOT NULL,
    charging_until   DATETIME(6)                                                                  NULL,
    carrier          VARCHAR(60)                                                                  NOT NULL DEFAULT '',
    tracking         VARCHAR(80)                                                                  NOT NULL DEFAULT '',
    cancel_reason    VARCHAR(200)                                                                 NOT NULL DEFAULT '',
    created_at       DATETIME(6)                                                                  NOT NULL,
    updated_at       DATETIME(6)                                                                  NOT NULL,
    paid_at          DATETIME(6)                                                                  NULL,
    shipped_at       DATETIME(6)                                                                  NULL,
    delivered_at     DATETIME(6)                                                                  NULL,
    cancelled_at     DATETIME(6)                                                                  NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_orders_number (number),
    KEY idx_orders_account (account_id, created_at),
    KEY idx_orders_status (status, created_at),
    KEY idx_orders_overdue (status, payment_deadline)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE order_items (
    order_id   BINARY(16)     NOT NULL,
    line_no    INT            NOT NULL,
    variant_id BINARY(16)     NOT NULL,
    sku        VARCHAR(64)    NOT NULL,
    name       VARCHAR(200)   NOT NULL,
    options    VARCHAR(300)   NOT NULL DEFAULT '',
    unit_price DECIMAL(12, 2) NOT NULL,
    qty        INT            NOT NULL,
    line_total DECIMAL(12, 2) NOT NULL,
    PRIMARY KEY (order_id, line_no),
    CONSTRAINT fk_order_items_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE order_status_history (
    id          BINARY(16)   NOT NULL,
    order_id    BINARY(16)   NOT NULL,
    from_status VARCHAR(20)  NOT NULL DEFAULT '',
    to_status   VARCHAR(20)  NOT NULL,
    actor       VARCHAR(60)  NOT NULL,
    note        VARCHAR(200) NOT NULL DEFAULT '',
    at          DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    KEY idx_history_order (order_id, at),
    CONSTRAINT fk_history_order FOREIGN KEY (order_id) REFERENCES orders (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- One row per attempt. A Culqi charge id is recorded as confirmed at most
-- once (uk_payments_confirmed_ref), which makes webhook replays harmless.
CREATE TABLE payments (
    id              BINARY(16)                        NOT NULL,
    order_id        BINARY(16)                        NOT NULL,
    method          ENUM ('culqi', 'bank_transfer')   NOT NULL,
    status          ENUM ('confirmed', 'failed')      NOT NULL,
    amount          DECIMAL(12, 2)                    NOT NULL,
    currency        CHAR(3)                           NOT NULL,
    provider_ref    VARCHAR(100)                      NOT NULL DEFAULT '',
    source_type     VARCHAR(20)                       NOT NULL DEFAULT '',
    failure_code    VARCHAR(60)                       NOT NULL DEFAULT '',
    failure_message VARCHAR(300)                      NOT NULL DEFAULT '',
    reference       VARCHAR(60)                       NOT NULL DEFAULT '',
    confirmed_by    BINARY(16)                        NULL,
    created_at      DATETIME(6)                       NOT NULL,
    confirmed_ref   VARCHAR(100) GENERATED ALWAYS AS (IF(status = 'confirmed' AND provider_ref <> '', provider_ref, NULL)) STORED,
    PRIMARY KEY (id),
    UNIQUE KEY uk_payments_confirmed_ref (confirmed_ref),
    KEY idx_payments_order (order_id, created_at),
    CONSTRAINT fk_payments_order FOREIGN KEY (order_id) REFERENCES orders (id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
