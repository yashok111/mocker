-- P7a (DESIGN §34.3, decisions.md mocker-p7-api-design D3): the OpenAPI
-- operation fields a custom endpoint carries for the export — summary,
-- description, tags, operationId, deprecated, parameters — as ONE JSON
-- document, NULL for every row written before this migration. One column
-- and not six because none of the fields is ever queried, exactly as
-- `stream` (0005) holds one document; ADD-only (the third after 0006 and
-- 0007) because an absent document is the whole of "no operation fields"
-- and no CHECK is needed to pair it with anything.
ALTER TABLE custom_endpoints ADD COLUMN operation TEXT;
