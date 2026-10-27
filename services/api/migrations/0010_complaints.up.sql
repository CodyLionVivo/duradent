-- Libro de Reclamaciones virtual (Ley 29571 and its reglamento): every sheet
-- is numbered per year, kept with the provider's details as they were when it
-- was filed, and answered within 15 business days. Sheets are never deleted.

CREATE TABLE complaint_sequences (
    year        SMALLINT NOT NULL,
    last_number INT      NOT NULL,
    PRIMARY KEY (year)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE complaints (
    id               BINARY(16)   NOT NULL,
    year             SMALLINT     NOT NULL,
    number           INT          NOT NULL,
    kind             ENUM ('reclamo', 'queja') NOT NULL,
    -- The signed-in account that filed it, if any (sheets can be anonymous).
    account_id       BINARY(16)   NULL,
    consumer_name    VARCHAR(120) NOT NULL,
    document_type    VARCHAR(3)   NOT NULL,
    document_number  VARCHAR(15)  NOT NULL,
    address          VARCHAR(200) NOT NULL,
    phone            VARCHAR(30)  NOT NULL DEFAULT '',
    email            VARCHAR(254) NOT NULL,
    guardian         VARCHAR(120) NOT NULL DEFAULT '',
    item_type        ENUM ('producto', 'servicio') NOT NULL,
    order_number     VARCHAR(20)  NOT NULL DEFAULT '',
    currency         CHAR(3)      NOT NULL DEFAULT 'PEN',
    amount           DECIMAL(12, 2) NULL,
    item_description VARCHAR(500) NOT NULL,
    detail           TEXT         NOT NULL,
    request          TEXT         NOT NULL,
    response_channel ENUM ('email', 'address') NOT NULL,
    locale           VARCHAR(5)   NOT NULL,
    provider_name    VARCHAR(120) NOT NULL,
    provider_ruc     CHAR(11)     NOT NULL,
    provider_address VARCHAR(200) NOT NULL,
    status           ENUM ('open', 'answered') NOT NULL,
    response         TEXT         NOT NULL,
    answered_by      VARCHAR(254) NOT NULL DEFAULT '',
    answered_at      DATETIME(6)  NULL,
    -- A retried submission (double click, reload) returns the same sheet.
    idempotency_key  VARCHAR(64)  NULL,
    filed_at         DATETIME(6)  NOT NULL,
    due_at           DATETIME(6)  NOT NULL,
    updated_at       DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_complaints_number (year, number),
    UNIQUE KEY uk_complaints_idempotency (idempotency_key),
    KEY idx_complaints_status (status, due_at),
    KEY idx_complaints_filed (filed_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
