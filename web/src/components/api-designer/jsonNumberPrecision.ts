// Call after JSON syntax validation; quoted digits are not numeric tokens.
export function hasUnsafeJsonNumber(source: string): boolean {
  for (let index = 0; index < source.length; index += 1) {
    if (source[index] === '"') {
      index += 1;
      while (index < source.length && source[index] !== '"') {
        if (source[index] === "\\") index += 1;
        index += 1;
      }
      continue;
    }
    if (source[index] !== "-" && !/\d/.test(source[index] ?? "")) continue;
    const token = source.slice(index).match(/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/)?.[0];
    if (token === undefined) continue;
    const value = Number(token);
    const rendered = Number.isFinite(value) ? JSON.stringify(value) : undefined;
    if (rendered === undefined || !sameJsonNumberValue(token, rendered)) return true;
    index += token.length - 1;
  }
  return false;
}

function sameJsonNumberValue(left: string, right: string): boolean {
  const leftFraction = decimalFraction(left);
  const rightFraction = decimalFraction(right);
  if (leftFraction === null || rightFraction === null) return false;
  return (
    leftFraction.numerator * rightFraction.denominator ===
    rightFraction.numerator * leftFraction.denominator
  );
}

function decimalFraction(token: string): { numerator: bigint; denominator: bigint } | null {
  const match = token.match(/^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/);
  if (match === null) return null;
  const fraction = match[3] ?? "";
  const exponent = Number(match[4] ?? 0) - fraction.length;
  if (!Number.isSafeInteger(exponent) || Math.abs(exponent) > 10_000) return null;
  let numerator = BigInt(`${match[1] ?? ""}${match[2] ?? "0"}${fraction}`);
  if (exponent >= 0) {
    numerator *= 10n ** BigInt(exponent);
    return { numerator, denominator: 1n };
  }
  return { numerator, denominator: 10n ** BigInt(-exponent) };
}
