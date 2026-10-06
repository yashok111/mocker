import { describe, it, expect } from "vitest";
import {
  observationPinKey,
  observationDisplay,
  observationRequestGate,
} from "./backendObservationReads";
describe("immutable observation reads", () => {
  it("keeps zero distinct from missing and rejects unsafe versions", () => {
    expect(observationDisplay(0)).toBe("0");
    expect(observationDisplay(undefined)).toBe("Неизвестно");
    expect(() =>
      observationPinKey({ setId: "s", version: 9007199254740992, contentHash: "h" }),
    ).toThrow();
    expect(observationPinKey({ setId: "s", version: 1, contentHash: "h" })).not.toBe(
      observationPinKey({ setId: "s", version: 2, contentHash: "h" }),
    );
  });
  it("invalidates a late response when another pin is selected", () => {
    const gate = observationRequestGate();
    const old = gate.begin();
    gate.begin();
    expect(gate.current(old)).toBe(false);
  });
});
