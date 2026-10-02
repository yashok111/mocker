// @vitest-environment node
import { describe, expect, it } from "vitest";
import { wrapParticipantName } from "./participantName";

const measure = (text: string) => Array.from(text).length * 9;

describe("participant name wrapping", () => {
  it("wraps the full name at word boundaries without ellipsis", () => {
    expect(wrapParticipantName("Platform API · auth + quiz", measure)).toEqual([
      "Platform API",
      "· auth + quiz",
    ]);
  });

  it("wraps long tokens without losing characters", () => {
    const name = "VeryLongParticipantNameWithoutSpaces";
    const lines = wrapParticipantName(name, measure);
    expect(lines.length).toBeGreaterThan(1);
    expect(lines.join("")).toBe(name);
    expect(lines.every((line) => measure(line) <= 120)).toBe(true);
  });

  it("retains explicit line breaks, including blank lines", () => {
    expect(wrapParticipantName("API\n\nAuth", measure)).toEqual(["API", "", "Auth"]);
    expect(wrapParticipantName("", measure)).toEqual([""]);
  });
});
