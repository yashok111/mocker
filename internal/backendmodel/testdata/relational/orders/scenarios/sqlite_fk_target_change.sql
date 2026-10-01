-- Named endpoint-conflict fixture. A replacement declaration only; do not apply.
CREATE TABLE main.order_items (
    tenant_id INTEGER NOT NULL,
    order_id INTEGER NOT NULL,
    line_no INTEGER NOT NULL,
    sku TEXT NOT NULL,
    quantity INTEGER NOT NULL DEFAULT 1,
    CONSTRAINT order_items_pk PRIMARY KEY (tenant_id, order_id, line_no),
    CONSTRAINT order_items_order_fk FOREIGN KEY (tenant_id, order_id)
        REFERENCES users (tenant_id, id) ON UPDATE CASCADE ON DELETE RESTRICT
        NOT DEFERRABLE INITIALLY IMMEDIATE,
    CONSTRAINT order_items_quantity_check CHECK (quantity > 0)
);
