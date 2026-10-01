-- Reviewed source delta only; do not apply it.
DROP VIEW public.order_summaries;
DROP INDEX public.orders_legacy_note_idx;
ALTER TABLE public.orders RENAME COLUMN status TO state;
ALTER TABLE public.orders DROP COLUMN legacy_note;
ALTER TABLE public.order_items DROP CONSTRAINT order_items_order_fk;
ALTER TABLE public.order_items ADD CONSTRAINT order_items_order_fk
    FOREIGN KEY (tenant_id, order_id) REFERENCES orders (tenant_id, id)
    ON UPDATE CASCADE ON DELETE RESTRICT NOT DEFERRABLE INITIALLY IMMEDIATE;
CREATE VIEW public.order_summaries AS
    SELECT tenant_id, id, state, total FROM orders WHERE state <> 'cancelled';
