import { describe, expect, it } from "vitest";
import type { BackendReadTarget } from "@/api/generated/schemas";
import {
  backendReadTargetKey,
  checkBackendReadPins,
  backendReadTargetFrom,
  checkBackendProjectionPins,
} from "./backendReadTargets";

const id = "0197aaf9-5555-7000-8000-000000000001";
const other = "0197aaf9-5555-7000-8000-000000000002";
const hash = "a".repeat(64);
const targets = [
  { revisionId: id },
  { proposal: { proposalId: id, proposalRevisionId: other } },
  { changeProposal: { proposalId: id, proposalRevisionId: other } },
  { importCandidate: { importId: id, importVersion: 3, candidateHash: hash } },
] as const satisfies readonly BackendReadTarget[];
const pins = {
  targetHash: hash,
  viewSchemaVersion: "import-candidate-v1",
  structuralSchemaVersion: "6",
  effectiveSemanticHash: "b".repeat(64),
  baseRevisionId: other,
  baseSemanticHash: "c".repeat(64),
  sourceVectorHash: "d".repeat(64),
  sourceSnapshotIds: [],
  artifactPins: [],
  artifactContext: null,
} as const;

describe("exact backend read targets", () => {
  it("extracts a request target without accepting mixed or null selectors", () => {
    expect(backendReadTargetFrom({ revisionId: id, limit: 10 })).toEqual({ revisionId: id });
    expect(() => backendReadTargetFrom({ revisionId: id, proposal: null, limit: 10 })).toThrow();
    expect(() => backendReadTargetFrom({ limit: 10 })).toThrow();
    expect(() => backendReadTargetFrom({ ...targets[2], ...targets[3] })).toThrow();
  });

  it("checks specialized view pins without requiring the basic-read envelope tag", () => {
    expect(checkBackendProjectionPins({ target: targets[3], pins }, targets[3])).toEqual(pins);
    expect(() => checkBackendReadPins({ target: targets[3], pins }, targets[3])).toThrow();
    expect(() =>
      checkBackendProjectionPins(
        { target: targets[3], pins: { ...pins, targetHash: "" } },
        targets[3],
      ),
    ).toThrow();
  });
  it("separates all four namespaces and each candidate version", () => {
    expect(new Set(targets.map(backendReadTargetKey)).size).toBe(4);
    expect(
      backendReadTargetKey({
        importCandidate: { importId: id, importVersion: 4, candidateHash: hash },
      }),
    ).not.toBe(backendReadTargetKey(targets[3]));
  });

  it("rejects mixed, incomplete and unsafe browser targets", () => {
    const invalid: unknown[] = [
      {},
      { revisionId: id, proposal: null },
      { revisionId: "" },
      { revisionId: "00000000-0000-0000-0000-000000000000" },
      { proposal: { proposalId: id } },
      { importCandidate: { importId: id, importVersion: 1, candidateHash: "old" } },
      {
        importCandidate: {
          importId: id,
          importVersion: Number.MAX_SAFE_INTEGER + 1,
          candidateHash: hash,
        },
      },
      { importCandidate: { importId: id, importVersion: "3", candidateHash: hash } },
      { changeProposal: { proposalId: id, proposalRevisionId: other, head: true } },
    ];
    for (const target of invalid) {
      expect(() => backendReadTargetKey(target as BackendReadTarget)).toThrow();
    }
    for (let i = 0; i < targets.length; i++) {
      for (const right of targets.slice(i + 1)) {
        expect(() =>
          backendReadTargetKey({ ...targets[i], ...right } as BackendReadTarget),
        ).toThrow();
      }
    }
  });

  it("requires exact versioned pins for candidates without falling back to source", () => {
    const target = targets[3];
    const response = { target, pins, viewSchemaVersion: "import-candidate-v1" };
    expect(() => checkBackendReadPins(response, target)).not.toThrow();
    for (const wrong of [
      { nodes: [], edges: [], nextCursor: "" },
      { ...response, viewSchemaVersion: "6" },
      { ...response, target: targets[0] },
      { ...response, pins: { ...pins, targetHash: "" } },
      { ...response, pins: { ...pins, viewSchemaVersion: "proposal-graph-v1" } },
    ]) {
      expect(() => checkBackendReadPins(wrong, target)).toThrow();
    }
    expect(() =>
      checkBackendReadPins(response, {
        importCandidate: { importId: id, importVersion: 4, candidateHash: hash },
      }),
    ).toThrow();
  });

  it("keeps old source wire but rejects a source-only answer to a legacy proposal", () => {
    expect(() => checkBackendReadPins({ nodes: [], edges: [] }, targets[0])).not.toThrow();
    expect(() => checkBackendReadPins({ nodes: [], edges: [] }, targets[1])).toThrow();
    expect(() =>
      checkBackendReadPins(
        {
          viewSchemaVersion: "proposal-relational-v1",
          proposalPins: { proposalId: id, proposalRevisionId: other },
        },
        targets[1],
      ),
    ).not.toThrow();
  });

  it("requires every page to retain the first response's complete pins", () => {
    const target = targets[3];
    const response = { target, pins, viewSchemaVersion: "import-candidate-v1" };
    const expected = checkBackendReadPins(response, target)!;
    expect(() =>
      checkBackendReadPins(
        { ...response, pins: { ...pins, sourceSnapshotIds: [id] } },
        target,
        expected,
      ),
    ).toThrow();
    expect(() =>
      checkBackendReadPins(
        { ...response, pins: { ...pins, artifactPins: [{ artifactId: id }] } },
        target,
        expected,
      ),
    ).toThrow();
    expect(() => checkBackendReadPins({}, targets[0], expected)).toThrow();
  });

  it("compares object members independently of their wire order", () => {
    const response = {
      target: targets[3],
      viewSchemaVersion: "import-candidate-v1",
      pins: { ...pins, artifactContext: { version: "1", bindings: [] } },
    };
    const expected = checkBackendReadPins(response, targets[3])!;
    expect(() =>
      checkBackendReadPins(
        {
          ...response,
          pins: { ...response.pins, artifactContext: { bindings: [], version: "1" } },
        },
        targets[3],
        expected,
      ),
    ).not.toThrow();
  });
});
