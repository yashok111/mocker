function object(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

// Known wire fields only: arbitrary attributes can contain prose, never code.
// Keep each facet separate because declared and effective definitions may differ.
export function nativeSources(attributes: unknown) {
  const root = object(attributes);
  const snippets: { field: string; label: string; text: string }[] = [];
  const add = (field: string, text: unknown, label = "Определение") => {
    if (typeof text === "string" && text.trim()) snippets.push({ field, label, text });
  };
  add("nativeText", root.nativeText);
  add("nativeDefinition", root.nativeDefinition);
  add("definition", root.definition);
  for (const [prefix, value] of [
    ["", root],
    ["relational.", root.relational],
    ["databaseRoutine.", root.databaseRoutine],
  ] as const) {
    for (const [facet, data] of Object.entries(object(object(value).facets))) {
      add(`${prefix}facets.${facet}.nativeDefinition`, object(data).nativeDefinition, facet);
      add(`${prefix}facets.${facet}.definition`, object(data).definition, facet);
    }
  }
  return snippets;
}
