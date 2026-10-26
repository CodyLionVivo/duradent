-- Identity: accounts, linked OIDC identities, sessions, billing profiles,
-- addresses and in-flight OIDC login states.

CREATE TABLE accounts (
    id             BINARY(16)                    NOT NULL,
    email          VARCHAR(254)                  NOT NULL,
    email_verified BOOLEAN                       NOT NULL DEFAULT FALSE,
    password_hash  VARCHAR(255)                  NOT NULL DEFAULT '',
    display_name   VARCHAR(120)                  NOT NULL,
    locale         VARCHAR(8)                    NOT NULL DEFAULT 'es',
    role           ENUM ('customer', 'admin')    NOT NULL DEFAULT 'customer',
    status         ENUM ('active', 'disabled')   NOT NULL DEFAULT 'active',
    created_at     DATETIME(6)                   NOT NULL,
    updated_at     DATETIME(6)                   NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_accounts_email (email)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE oauth_identities (
    provider   VARCHAR(16)  NOT NULL,
    subject    VARCHAR(255) NOT NULL,
    account_id BINARY(16)   NOT NULL,
    email      VARCHAR(254) NOT NULL DEFAULT '',
    created_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (provider, subject),
    KEY idx_oauth_identities_account (account_id),
    CONSTRAINT fk_oauth_identities_account FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- Only the SHA-256 of each session token is stored.
CREATE TABLE sessions (
    token_hash CHAR(64)     NOT NULL,
    account_id BINARY(16)   NOT NULL,
    created_at DATETIME(6)  NOT NULL,
    expires_at DATETIME(6)  NOT NULL,
    ip         VARCHAR(45)  NOT NULL DEFAULT '',
    user_agent VARCHAR(255) NOT NULL DEFAULT '',
    PRIMARY KEY (token_hash),
    KEY idx_sessions_account (account_id),
    KEY idx_sessions_expires (expires_at),
    CONSTRAINT fk_sessions_account FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- price_list_code is a reference by code to pricing.price_lists, without a
-- foreign key: contexts do not share schema constraints.
CREATE TABLE customers (
    account_id      BINARY(16)                  NOT NULL,
    customer_type   ENUM ('persona', 'empresa') NOT NULL,
    document_type   ENUM ('DNI', 'RUC', 'CE')   NOT NULL,
    document_number VARCHAR(12)                 NOT NULL,
    business_name   VARCHAR(200)                NOT NULL DEFAULT '',
    cop_number      VARCHAR(6)                  NOT NULL DEFAULT '',
    phone           VARCHAR(20)                 NOT NULL DEFAULT '',
    price_list_code VARCHAR(32)                 NOT NULL DEFAULT '',
    updated_at      DATETIME(6)                 NOT NULL,
    PRIMARY KEY (account_id),
    KEY idx_customers_document (document_type, document_number),
    CONSTRAINT fk_customers_account FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE addresses (
    id         BINARY(16)   NOT NULL,
    account_id BINARY(16)   NOT NULL,
    label      VARCHAR(60)  NOT NULL DEFAULT '',
    recipient  VARCHAR(120) NOT NULL,
    line1      VARCHAR(200) NOT NULL,
    reference  VARCHAR(200) NOT NULL DEFAULT '',
    district   VARCHAR(80)  NOT NULL,
    province   VARCHAR(80)  NOT NULL,
    department CHAR(2)      NOT NULL,
    ubigeo     CHAR(6)      NOT NULL DEFAULT '',
    phone      VARCHAR(20)  NOT NULL DEFAULT '',
    is_default BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    KEY idx_addresses_account (account_id),
    CONSTRAINT fk_addresses_account FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE login_states (
    state        VARCHAR(64)  NOT NULL,
    provider     VARCHAR(16)  NOT NULL,
    nonce        VARCHAR(64)  NOT NULL,
    verifier     VARCHAR(128) NOT NULL,
    redirect_uri VARCHAR(512) NOT NULL,
    return_to    VARCHAR(512) NOT NULL DEFAULT '/',
    expires_at   DATETIME(6)  NOT NULL,
    PRIMARY KEY (state),
    KEY idx_login_states_expires (expires_at)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
