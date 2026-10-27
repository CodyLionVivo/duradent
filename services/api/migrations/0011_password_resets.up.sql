-- "Forgot your password?": single-use links valid for one hour. Only the
-- SHA-256 of the emailed token is stored; using one link deletes every
-- pending link of the account.
CREATE TABLE password_resets (
    token_hash CHAR(64)     NOT NULL, -- sha256 hex of the emailed token
    account_id BINARY(16)   NOT NULL,
    email      VARCHAR(254) NOT NULL, -- the address the link was sent to
    expires_at DATETIME(6)  NOT NULL,
    created_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (token_hash),
    KEY idx_password_resets_account (account_id, created_at),
    CONSTRAINT fk_password_resets_account FOREIGN KEY (account_id) REFERENCES accounts (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
