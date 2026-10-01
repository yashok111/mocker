-- Reviewed source delta only; do not apply it.
DROP VIEW main.order_summaries;
DROP INDEX main.orders_legacy_note_idx;
ALTER TABLE main.orders RENAME COLUMN status TO state;
ALTER TABLE main.orders DROP COLUMN legacy_note;
-- SQLite changes a declared FK action by replacing the child table.
CREATE TABLE main.order_items_replacement (
    tenant_id INTEGER NOT NULL,
    order_id INTEGER NOT NULL,
    line_no INTEGER NOT NULL,
    sku TEXT NOT NULL,
    quantity INTEGER NOT NULL DEFAULT 1,
    CONSTRAINT order_items_pk PRIMARY KEY (tenant_id, order_id, line_no),
    CONSTRAINT order_items_order_fk FOREIGN KEY (tenant_id, order_id)
        REFERENCES orders (tenant_id, id) ON UPDATE CASCADE ON DELETE RESTRICT
        NOT DEFERRABLE INITIALLY IMMEDIATE,
    CONSTRAINT order_items_quantity_check CHECK (quantity > 0)
);
INSERT INTO main.order_items_replacement
    SELECT tenant_id, order_id, line_no, sku, quantity FROM main.order_items;
DROP TABLE main.order_items;
ALTER TABLE main.order_items_replacement RENAME TO order_items;
CREATE VIEW main.order_summaries AS
    SELECT tenant_id, id, state, total FROM orders WHERE state <> 'cancelled';
