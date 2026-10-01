-- Named endpoint-conflict fixture. Source evidence only; do not apply.
ALTER TABLE public.order_items DROP CONSTRAINT order_items_order_fk;
ALTER TABLE public.order_items ADD CONSTRAINT order_items_order_fk
    FOREIGN KEY (tenant_id, order_id) REFERENCES users (tenant_id, id)
    ON UPDATE CASCADE ON DELETE RESTRICT NOT DEFERRABLE INITIALLY IMMEDIATE;
