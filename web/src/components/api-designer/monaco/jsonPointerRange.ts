export type JsonSourceRange = {
  startOffset: number;
  endOffset: number;
};

export function findJsonPointerRange(source: string, pointer: string): JsonSourceRange | null {
  const target = pointerTokens(pointer);
  if (target === null) return null;

  let offset = 0;
  let found: JsonSourceRange | null = null;

  const skipWhitespace = (): void => {
    while (/\s/.test(source[offset] ?? "")) offset += 1;
  };

  const readString = (): { value: string; start: number; end: number } => {
    const start = offset;
    if (source[offset] !== '"') throw new Error("expected JSON string");
    offset += 1;
    while (offset < source.length) {
      if (source[offset] === "\\") {
        offset += 2;
        continue;
      }
      if (source[offset] === '"') {
        offset += 1;
        return { value: JSON.parse(source.slice(start, offset)) as string, start, end: offset };
      }
      offset += 1;
    }
    throw new Error("unterminated JSON string");
  };

  const parseValue = (path: string[], rangeStart?: number): void => {
    skipWhitespace();
    const valueStart = offset;
    const matches = samePath(path, target);
    const token = source[offset];

    if (token === "{") {
      offset += 1;
      skipWhitespace();
      while (source[offset] !== "}") {
        const key = readString();
        skipWhitespace();
        if (source[offset] !== ":") throw new Error("expected colon");
        offset += 1;
        parseValue([...path, key.value], key.start);
        skipWhitespace();
        if (source[offset] === ",") {
          offset += 1;
          skipWhitespace();
          continue;
        }
        if (source[offset] !== "}") throw new Error("expected object end");
      }
      offset += 1;
    } else if (token === "[") {
      offset += 1;
      skipWhitespace();
      let index = 0;
      while (source[offset] !== "]") {
        parseValue([...path, String(index)]);
        index += 1;
        skipWhitespace();
        if (source[offset] === ",") {
          offset += 1;
          skipWhitespace();
          continue;
        }
        if (source[offset] !== "]") throw new Error("expected array end");
      }
      offset += 1;
    } else if (token === '"') {
      readString();
    } else {
      while (offset < source.length && !/[\s,}\]]/.test(source[offset] ?? "")) offset += 1;
      if (offset === valueStart) throw new Error("expected JSON value");
    }

    if (matches && found === null) {
      found = { startOffset: rangeStart ?? valueStart, endOffset: offset };
    }
  };

  try {
    parseValue([]);
    skipWhitespace();
    return offset === source.length ? found : null;
  } catch {
    return null;
  }
}

function pointerTokens(pointer: string): string[] | null {
  if (pointer === "") return [];
  if (!pointer.startsWith("/")) return null;
  return pointer
    .slice(1)
    .split("/")
    .map((token) => token.replaceAll("~1", "/").replaceAll("~0", "~"));
}

function samePath(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((token, index) => token === right[index]);
}
