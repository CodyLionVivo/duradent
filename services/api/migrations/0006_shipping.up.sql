-- Shipping rates by zone and parcel weight, in both currencies (prices
-- without IGV, like the catalog). Default rates are seeded here so every
-- environment can quote delivery; adjust them with SQL until an admin screen
-- exists.

CREATE TABLE shipping_rates (
    zone         ENUM ('lima', 'provincias') NOT NULL,
    max_weight_g INT UNSIGNED                NOT NULL,
    amount_pen   DECIMAL(12, 2)              NOT NULL,
    amount_usd   DECIMAL(12, 2)              NOT NULL,
    PRIMARY KEY (zone, max_weight_g)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

INSERT INTO shipping_rates (zone, max_weight_g, amount_pen, amount_usd) VALUES
    ('lima', 1000, 10.00, 2.70),
    ('lima', 5000, 15.00, 4.00),
    ('lima', 20000, 25.00, 6.70),
    ('provincias', 1000, 18.00, 4.80),
    ('provincias', 5000, 28.00, 7.50),
    ('provincias', 20000, 45.00, 12.00);
