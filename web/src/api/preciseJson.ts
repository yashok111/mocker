const unsafeNumberMessage =
  "Документ содержит числа, которые браузер не может сохранить без потери точности. Используйте MCP для работы с этим документом.";

export class BrowserJsonPrecisionError extends Error {
  static readonly userMessage = unsafeNumberMessage;

  constructor() {
    super(BrowserJsonPrecisionError.userMessage);
    this.name = "BrowserJsonPrecisionError";
  }
}

const jsonNumberPattern = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/y;
const decimalPartsPattern = /^(-?)(\d+)(?:\.(\d+))?(?:[eE]([+-]?\d+))?$/;

function normalizeDecimal(token: string): string {
  const match = decimalPartsPattern.exec(token);
  if (match === null) {
    throw new SyntaxError(`Invalid JSON number: ${token}`);
  }

  const [, sign, integer, fraction = "", exponent = "0"] = match;
  let digits = `${integer}${fraction}`.replace(/^0+/, "");
  if (digits === "") {
    return "0";
  }

  let decimalExponent = BigInt(exponent) - BigInt(fraction.length);
  const trailingZeros = /0+$/.exec(digits)?.[0].length ?? 0;
  if (trailingZeros > 0) {
    digits = digits.slice(0, -trailingZeros);
    decimalExponent += BigInt(trailingZeros);
  }

  return `${sign}${digits}e${decimalExponent}`;
}

function assertBrowserSafeNumber(token: string): void {
  const value = Number(token);
  if (!Number.isFinite(value)) {
    throw new BrowserJsonPrecisionError();
  }

  const browserToken = JSON.stringify(value);
  if (browserToken === undefined || normalizeDecimal(token) !== normalizeDecimal(browserToken)) {
    throw new BrowserJsonPrecisionError();
  }
}

function assertBrowserSafeNumbers(text: string): void {
  for (let index = 0; index < text.length;) {
    const character = text[index]!;
    if (character === '"') {
      index += 1;
      while (index < text.length) {
        if (text[index] === "\\") {
          index += 2;
        } else if (text[index] === '"') {
          index += 1;
          break;
        } else {
          index += 1;
        }
      }
      continue;
    }

    if (character === "-" || (character >= "0" && character <= "9")) {
      jsonNumberPattern.lastIndex = index;
      const match = jsonNumberPattern.exec(text);
      if (match !== null) {
        assertBrowserSafeNumber(match[0]);
        index = jsonNumberPattern.lastIndex;
        continue;
      }
    }

    index += 1;
  }
}

export function parseBrowserSafeJson(text: string): unknown {
  const parsed: unknown = JSON.parse(text);
  assertBrowserSafeNumbers(text);
  return parsed;
}
