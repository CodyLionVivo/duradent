-- Catalog: categories, products with localized copy, variants described by
-- attribute values, and product images stored in S3.

CREATE TABLE categories (
    id         BINARY(16)   NOT NULL,
    slug       VARCHAR(120) NOT NULL,
    parent_id  BINARY(16)   NULL,
    sort_order INT          NOT NULL DEFAULT 0,
    created_at DATETIME(6)  NOT NULL,
    updated_at DATETIME(6)  NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_categories_slug (slug),
    KEY idx_categories_parent (parent_id),
    CONSTRAINT fk_categories_parent FOREIGN KEY (parent_id) REFERENCES categories (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE category_translations (
    category_id BINARY(16)   NOT NULL,
    locale      VARCHAR(8)   NOT NULL,
    name        VARCHAR(200) NOT NULL,
    description TEXT         NOT NULL,
    PRIMARY KEY (category_id, locale),
    CONSTRAINT fk_category_translations FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE products (
    id          BINARY(16)                          NOT NULL,
    slug        VARCHAR(120)                        NOT NULL,
    brand       VARCHAR(80)                         NOT NULL DEFAULT '',
    status      ENUM ('draft', 'active', 'archived') NOT NULL DEFAULT 'draft',
    category_id BINARY(16)                          NULL,
    created_at  DATETIME(6)                         NOT NULL,
    updated_at  DATETIME(6)                         NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_products_slug (slug),
    KEY idx_products_status_category (status, category_id),
    CONSTRAINT fk_products_category FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE product_translations (
    product_id  BINARY(16)   NOT NULL,
    locale      VARCHAR(8)   NOT NULL,
    name        VARCHAR(200) NOT NULL,
    description TEXT         NOT NULL,
    PRIMARY KEY (product_id, locale),
    KEY idx_product_translations_name (locale, name),
    CONSTRAINT fk_product_translations FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE attributes (
    id         BINARY(16)  NOT NULL,
    code       VARCHAR(32) NOT NULL,
    names      JSON        NOT NULL, -- {"es": "Pieza", "en": "Tooth", ...}
    sort_order INT         NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    UNIQUE KEY uk_attributes_code (code)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE attribute_values (
    id           BINARY(16)  NOT NULL,
    attribute_id BINARY(16)  NOT NULL,
    code         VARCHAR(32) NOT NULL,
    labels       JSON        NOT NULL,
    sort_order   INT         NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    UNIQUE KEY uk_attribute_values_code (attribute_id, code),
    CONSTRAINT fk_attribute_values_attribute FOREIGN KEY (attribute_id) REFERENCES attributes (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

-- Variants are archived, never deleted: prices, stock and orders reference them.
CREATE TABLE variants (
    id          BINARY(16)   NOT NULL,
    product_id  BINARY(16)   NOT NULL,
    sku         VARCHAR(64)  NOT NULL,
    barcode     VARCHAR(64)  NOT NULL DEFAULT '',
    weight_g    INT UNSIGNED NOT NULL DEFAULT 0,
    is_default  BOOLEAN      NOT NULL DEFAULT FALSE,
    archived_at DATETIME(6)  NULL,
    sort_order  INT          NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    UNIQUE KEY uk_variants_sku (sku),
    KEY idx_variants_product (product_id, sort_order),
    CONSTRAINT fk_variants_product FOREIGN KEY (product_id) REFERENCES products (id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE variant_attribute_values (
    variant_id         BINARY(16) NOT NULL,
    attribute_value_id BINARY(16) NOT NULL,
    PRIMARY KEY (variant_id, attribute_value_id),
    KEY idx_vav_value (attribute_value_id),
    CONSTRAINT fk_vav_variant FOREIGN KEY (variant_id) REFERENCES variants (id) ON DELETE CASCADE,
    CONSTRAINT fk_vav_value FOREIGN KEY (attribute_value_id) REFERENCES attribute_values (id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE media (
    id         BINARY(16)   NOT NULL,
    product_id BINARY(16)   NOT NULL,
    s3_key     VARCHAR(255) NOT NULL,
    alt        JSON         NOT NULL,
    sort_order INT          NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    KEY idx_media_product (product_id, sort_order),
    CONSTRAINT fk_media_product FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
