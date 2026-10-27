-- Dollar prices are not restored; shipping gets its column back at 0.
ALTER TABLE shipping_rates ADD COLUMN amount_usd DECIMAL(12, 2) NOT NULL DEFAULT 0 AFTER amount_pen;
