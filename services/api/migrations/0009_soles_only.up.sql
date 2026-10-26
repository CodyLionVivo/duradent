-- The store sells only in soles: drop the dollar prices and shipping rates.
DELETE FROM prices WHERE currency <> 'PEN';
ALTER TABLE shipping_rates DROP COLUMN amount_usd;
