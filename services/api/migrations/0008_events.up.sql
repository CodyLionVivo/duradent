-- Phase 5: emails sent by the worker, SUNAT electronic documents, refunds of
-- paid orders and email verification.

-- One row per (event, email kind): the worker checks it before sending so a
-- redelivered event does not email the customer twice.
CREATE TABLE email_log (
    id           BINARY(16)   NOT NULL,
    event_id     BINARY(16)   NOT NULL,
    kind         VARCHAR(40)  NOT NULL,
    recipient    VARCHAR(254) NOT NULL,
    subject      VARCHAR(300) NOT NULL,
    order_number VARCHAR(20)  NOT NULL DEFAULT '',
    status       ENUM ('sending', 'sent', 'failed') NOT NULL,
    provider_id  VARCHAR(200) NOT NULL DEFAULT '',
    error        VARCHAR(1000) NOT NULL DEFAULT '',
    created_at   DATETIME(6)  NOT NULL,
    sent_at      DATETIME(6)  NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_email_log_event (event_id, kind, recipient),
    KEY idx_email_log_order (order_number),
    KEY idx_email_log_created (created_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- Correlative numbers per series (F001, B001, ...). SUNAT requires them
-- without gaps, so a number is taken inside the transaction that creates the
-- invoice row.
CREATE TABLE invoice_series (
    series      CHAR(4) NOT NULL,
    last_number INT     NOT NULL,
    PRIMARY KEY (series)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE invoices (
    id               BINARY(16)   NOT NULL,
    order_id         BINARY(16)   NOT NULL,
    order_number     VARCHAR(20)  NOT NULL,
    doc_type         ENUM ('factura', 'boleta', 'nota_credito') NOT NULL,
    series           CHAR(4)      NOT NULL,
    number           INT          NOT NULL,
    -- Credit notes point at the document they cancel.
    ref_invoice_id   BINARY(16)   NULL,
    currency         CHAR(3)      NOT NULL,
    total            DECIMAL(12, 2) NOT NULL,
    status           ENUM ('pending', 'accepted', 'rejected') NOT NULL,
    attempts         INT          NOT NULL DEFAULT 0,
    sunat_description VARCHAR(500) NOT NULL DEFAULT '',
    hash             VARCHAR(100) NOT NULL DEFAULT '',
    pdf_url          VARCHAR(500) NOT NULL DEFAULT '',
    xml_url          VARCHAR(500) NOT NULL DEFAULT '',
    cdr_url          VARCHAR(500) NOT NULL DEFAULT '',
    last_error       VARCHAR(1000) NOT NULL DEFAULT '',
    created_at       DATETIME(6)  NOT NULL,
    updated_at       DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_invoices_number (series, number),
    -- At most one sale document and one credit note per order.
    UNIQUE KEY uk_invoices_order_kind (order_id, doc_type),
    KEY idx_invoices_status (status, updated_at),
    CONSTRAINT fk_invoices_order FOREIGN KEY (order_id) REFERENCES orders (id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- Refunds: a paid order is cancelled and its money returned.
ALTER TABLE orders
    ADD COLUMN refunded_at DATETIME(6) NULL AFTER cancelled_at,
    ADD COLUMN refund_ref  VARCHAR(100) NOT NULL DEFAULT '' AFTER refunded_at;

ALTER TABLE payments
    MODIFY COLUMN status ENUM ('confirmed', 'failed', 'refunded') NOT NULL;

CREATE TABLE email_verifications (
    token_hash CHAR(64)     NOT NULL, -- sha256 hex of the emailed token
    account_id BINARY(16)   NOT NULL,
    email      VARCHAR(254) NOT NULL,
    expires_at DATETIME(6)  NOT NULL,
    created_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (token_hash),
    KEY idx_email_verifications_account (account_id),
    CONSTRAINT fk_email_verifications_account FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

ALTER TABLE stock_reservations
    MODIFY COLUMN status ENUM ('active', 'confirmed', 'released', 'returned') NOT NULL DEFAULT 'active';
