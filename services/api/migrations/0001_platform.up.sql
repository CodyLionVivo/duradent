-- Transactional outbox: domain events written in the same transaction as the
-- state change, then published to SNS by the outbox-relay process.
CREATE TABLE outbox (
    id             BINARY(16)   NOT NULL,
    aggregate_type VARCHAR(64)  NOT NULL,
    aggregate_id   BINARY(16)   NOT NULL,
    event_type     VARCHAR(128) NOT NULL,
    payload        JSON         NOT NULL,
    occurred_at    DATETIME(6)  NOT NULL,
    published_at   DATETIME(6)  NULL,
    attempts       INT UNSIGNED NOT NULL DEFAULT 0,
    locked_until   DATETIME(6)  NULL,
    last_error     TEXT         NULL,
    PRIMARY KEY (id),
    KEY idx_outbox_pending (published_at, occurred_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- Consumer idempotency: a message is handled at most once per consumer.
CREATE TABLE processed_messages (
    consumer     VARCHAR(64) NOT NULL,
    message_id   BINARY(16)  NOT NULL,
    processed_at DATETIME(6) NOT NULL,
    PRIMARY KEY (consumer, message_id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- HTTP idempotency for unsafe requests (e.g. POST /checkout with Idempotency-Key).
CREATE TABLE idempotency_keys (
    idem_key      VARCHAR(128) NOT NULL,
    scope         VARCHAR(64)  NOT NULL,
    request_hash  CHAR(64)     NOT NULL,
    response_code SMALLINT     NULL,
    response_body JSON         NULL,
    created_at    DATETIME(6)  NOT NULL,
    expires_at    DATETIME(6)  NOT NULL,
    PRIMARY KEY (scope, idem_key),
    KEY idx_idempotency_expires (expires_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
