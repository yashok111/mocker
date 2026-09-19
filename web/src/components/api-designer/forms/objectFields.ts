export function omitEmpty(
  record: Record<string, unknown>,
  key: string,
  value: unknown,
): Record<string, unknown> {
  const next = { ...record };
  if (value === "" || value === undefined || value === false) delete next[key];
  else next[key] = value;
  return next;
}

export function renameKey(
  record: Record<string, unknown>,
  oldKey: string,
  newKey: string,
): Record<string, unknown> {
  if (oldKey === newKey || newKey === "" || newKey in record) return record;
  return Object.fromEntries(
    Object.entries(record).map(([key, value]) => [key === oldKey ? newKey : key, value]),
  );
}
