-- Source-only unsupported migration; no state replay is authorized.
-- Direct catalog mutation with an expression is deliberately not a handled change.
PRAGMA writable_schema = ON;
UPDATE sqlite_schema
    SET sql = replace(sql, ')', ', ' || upper('runtime_column') || ' TEXT)')
    WHERE type = 'table' AND name = 'orders';
PRAGMA writable_schema = OFF;
