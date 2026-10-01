-- Source-only unsupported migration; no state replay is authorized.
-- Static analysis cannot determine the schema after the dynamic operation.
DO $migration$
BEGIN
    EXECUTE 'ALTER TABLE public.orders ADD COLUMN ' ||
        current_setting('orders.fixture_column') || ' TEXT';
END
$migration$;
