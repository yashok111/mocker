import { expect, it } from "vitest";
import { ApiFailure } from "@/api/client";
import { captureImportAttempt, uncertainImportFailure } from "./backendImportAttempts";

it("retains a detached immutable request for the exact same-body retry", () => {
  const original = {
    kind: "batch" as const,
    projectId: "p",
    sessionId: "s",
    batchId: "stable",
    body: {
      expectedImportVersion: 3,
      payloadHash: "hash",
      commands: [
        { op: "remove" as const, remove: { recordType: "node" as const, externalKey: "old" } },
      ],
    },
  };
  const captured = captureImportAttempt(original);
  const wire = JSON.stringify(captured);
  original.body.expectedImportVersion = 9;
  original.body.commands[0]!.remove.externalKey = "changed";
  expect(JSON.stringify(captured)).toBe(wire);
  expect(Object.isFrozen(captured.body.commands[0]!.remove)).toBe(true);
});

it("separates uncertain transport outcomes from known CAS and validation refusals", () => {
  for (const error of [
    new TypeError("lost"),
    new ApiFailure("timeout", 408, "timeout"),
    new ApiFailure("unavailable", 503, "backend_unavailable"),
  ])
    expect(uncertainImportFailure(error)).toBe(true);
  for (const status of [400, 409, 413, 422])
    expect(uncertainImportFailure(new ApiFailure("rejected", status, "backend_invalid"))).toBe(
      false,
    );
});
