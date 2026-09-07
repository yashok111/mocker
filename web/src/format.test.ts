import { describe, expect, it } from "vitest";
import { formatBytes, formatBytesPerSec, formatTimestamp } from "./format";

// formatBytes had two disagreeing implementations until A21 merged them
// (AssetsPage always wrote one decimal, StreamEditor dropped it on an exact
// multiple). These cases pin WHICH rule survived, because the disagreement
// was invisible: both produced plausible Russian copy for the same number.
describe("formatBytes", () => {
  it.each([
    [0, "0 Б"],
    [512, "512 Б"],
    [1024, "1 КБ"],
    [1536, "1.5 КБ"],
    [32 * 1024, "32 КБ"],
    [1024 * 1024, "1 МБ"],
    [8 * 1024 * 1024, "8 МБ"],
    [1024 * 1024 + 512 * 1024, "1.5 МБ"],
  ])("%d → %s", (n, want) => {
    expect(formatBytes(n)).toBe(want);
  });
});

describe("formatBytesPerSec", () => {
  it("keeps one decimal even on an exact multiple: a rate is measured, not configured", () => {
    expect(formatBytesPerSec(2048)).toBe("2.0 КБ/с");
    expect(formatBytesPerSec(1024 * 1024)).toBe("1.0 МБ/с");
    expect(formatBytesPerSec(900)).toBe("900 Б/с");
  });
});

describe("formatTimestamp", () => {
  it("reads Unix SECONDS, not milliseconds", () => {
    // The whole point of the shared helper: dayjs(x) on a seconds value reads
    // 1970, which is what every screen's own copy existed to prevent.
    expect(formatTimestamp(1_700_000_000)).not.toContain("1970");
    expect(formatTimestamp(0)).toContain("1970");
  });
});
