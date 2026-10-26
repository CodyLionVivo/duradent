UPDATE stock_reservations SET status = 'confirmed' WHERE status = 'returned';
ALTER TABLE stock_reservations MODIFY COLUMN status ENUM ('active', 'confirmed', 'released') NOT NULL DEFAULT 'active';
DROP TABLE IF EXISTS email_verifications;
UPDATE payments SET status = 'confirmed' WHERE status = 'refunded';
ALTER TABLE payments MODIFY COLUMN status ENUM ('confirmed', 'failed') NOT NULL;
ALTER TABLE orders DROP COLUMN refund_ref, DROP COLUMN refunded_at;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS invoice_series;
DROP TABLE IF EXISTS email_log;
