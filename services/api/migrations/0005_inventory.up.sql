-- Inventory: stock per variant, reservations held for orders, and an audit
-- trail of every change to on-hand stock. variant_id has no foreign key: the
-- inventory does not depend on the catalog schema.

CREATE TABLE stock_items (
    variant_id    BINARY(16)  NOT NULL,
    on_hand       INT         NOT NULL DEFAULT 0,
    reserved      INT         NOT NULL DEFAULT 0,
    reorder_point INT         NOT NULL DEFAULT 0,
    updated_at    DATETIME(6) NOT NULL,
    PRIMARY KEY (variant_id),
    CONSTRAINT chk_stock_reserved CHECK (reserved >= 0 AND reserved <= on_hand)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE stock_reservations (
    id         BINARY(16)                                NOT NULL,
    order_id   BINARY(16)                                NOT NULL,
    variant_id BINARY(16)                                NOT NULL,
    qty        INT                                       NOT NULL,
    status     ENUM ('active', 'confirmed', 'released')  NOT NULL DEFAULT 'active',
    created_at DATETIME(6)                               NOT NULL,
    PRIMARY KEY (id),
    KEY idx_reservations_order (order_id, status),
    KEY idx_reservations_variant (variant_id, status)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE stock_movements (
    id         BINARY(16)                                         NOT NULL,
    variant_id BINARY(16)                                         NOT NULL,
    delta      INT                                                NOT NULL,
    reason     ENUM ('sale', 'adjustment', 'purchase', 'return')  NOT NULL,
    reference  VARCHAR(200)                                       NOT NULL DEFAULT '',
    created_at DATETIME(6)                                        NOT NULL,
    PRIMARY KEY (id),
    KEY idx_movements_variant (variant_id, created_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
