import type {
  AuthPresetProposal,
  AuthResponse,
  Binding,
  Condition,
  Directive,
  EndpointListView,
  EndpointView,
  MergedOperationView,
  MergedStatusView,
  OpOverrideSummaryView,
  OperationView,
  OverrideDocView,
  Recipe,
  ReportView,
  ServerConfigView,
  SessionListView,
  Settings,
  SpecImportView,
  SpecView,
  TrafficListView,
  TrafficPollView,
  TrafficRow,
  UserView,
  Variant,
  WarningView,
  WorkspaceView,
} from "@/api/generated/schemas";

// Fixtures for the generated wire types. They exist because those types are
// now COMPLETE — orval derives them from api/openapi.json, which describes
// every field the Go handlers actually send — so a test can no longer write
// `settings: {}` and have TypeScript shrug. Building the full object once here
// is the whole cost of that, and in exchange a field added to the contract
// fails compilation in one place instead of silently defaulting in twenty.

export function settingsFixture(overrides: Partial<Settings> = {}): Settings {
  return {
    seed: 1,
    basePath: "",
    listSize: 3,
    nullRate: 0,
    envelope: null,
    identity: { id: 1, name: "Alex", email: "alex@example.com", roles: ["user"] },
    auth: { jwtTtlSec: 3600, alg: "HS256", signingKey: "", requireHeader: false },
    cors: { mode: "reflect", credentials: true },
    validateRequests: false,
    delayMs: 0,
    ...overrides,
  };
}

export function workspaceFixture(overrides: Partial<WorkspaceView> = {}): WorkspaceView {
  return {
    id: 1,
    slug: "alex",
    name: "Alex",
    specId: null,
    ownerId: 1,
    forkedFrom: null,
    scenarioId: null,
    revision: 0,
    settings: settingsFixture(),
    url: "http://alex.mock.corp.internal:8080",
    createdAt: 0,
    updatedAt: 0,
    editVersion: 1,
    ...overrides,
  };
}

export function userFixture(overrides: Partial<UserView> = {}): UserView {
  return { id: 1, name: "alex", role: "user", createdAt: 0, ...overrides };
}

export function serverConfigFixture(overrides: Partial<ServerConfigView> = {}): ServerConfigView {
  return {
    // Deliberately NOT /__mocker: the reserved prefix is configurable, and a
    // test that used the default value would pass just as happily against a UI
    // that hard-coded it — which is the exact bug this field exists to prevent.
    reservedPrefix: "/__test-prefix",
    baseDomain: "mock.corp.internal",
    routing: "host",
    // A9: the effective limits, deliberately NOT the defaults for the same
    // reason as the prefix above — a strip that hard-coded 4 MiB would pass
    // against these numbers only if it read them.
    limits: {
      maxBodyBytes: 5 * 1024 * 1024,
      maxResponseBytes: 3 * 1024 * 1024,
      maxAssetBytes: 2 * 1024 * 1024,
      maxAssetsTotalBytes: 16 * 1024 * 1024,
      maxEntities: 500,
      trafficMaxBodyBytes: 4096,
      trafficRetention: 900,
      checkpointRetention: 15,
      checkpointDebounceSec: 120,
      streamMaxConns: 25,
      streamMaxLifetimeSec: 600,
      streamMaxFrameBytes: 32 * 1024,
      streamSendBudgetBytes: 128 * 1024,
      streamPingSec: 10,
      streamFrameTimeoutSec: 4,
      streamTrafficFrames: "off",
      streamTrafficMaxFrames: 100,
      streamTrafficMaxBytes: 32 * 1024,
    },
    ...overrides,
  };
}

export function authResponseFixture(overrides: Partial<AuthResponse> = {}): AuthResponse {
  return {
    user: userFixture(),
    csrfToken: "csrf-token-value",
    config: serverConfigFixture(),
    ...overrides,
  };
}

// --- Specs (§3.2) --------------------------------------------------------

export function warningViewFixture(overrides: Partial<WarningView> = {}): WarningView {
  return {
    pointer: "/paths/~1pets/get/responses/200",
    code: "unsupported_schema_type",
    message: "unsupported schema type, degraded to string",
    ...overrides,
  };
}

export function reportViewFixture(overrides: Partial<ReportView> = {}): ReportView {
  return {
    format: "oas31",
    basePath: "/v1",
    basePathOrigin: "servers[0].url",
    warnings: [warningViewFixture()],
    operations: 12,
    degraded: 1,
    ...overrides,
  };
}

export function specViewFixture(overrides: Partial<SpecView> = {}): SpecView {
  return {
    id: 1,
    name: "Petstore",
    version: "1.0.0",
    format: "oas31",
    source: "upload",
    sourceRef: "petstore.json",
    basePath: "/v1",
    hash: "deadbeefcafe",
    createdAt: 0,
    createdBy: 1,
    ...overrides,
  };
}

export function specImportViewFixture(overrides: Partial<SpecImportView> = {}): SpecImportView {
  return {
    ...specViewFixture(),
    duplicate: false,
    report: reportViewFixture(),
    ...overrides,
  };
}

export function operationViewFixture(overrides: Partial<OperationView> = {}): OperationView {
  return {
    id: 1,
    specId: 1,
    method: "GET",
    path: "/pets/{petId}",
    canonicalPath: "/pets/{id}",
    operationId: "getPet",
    summary: "Get a pet",
    tag: "pets",
    sourceOrder: 0,
    pointer: "/paths/~1pets~1{petId}/get",
    parseError: null,
    ...overrides,
  };
}

// --- Merged operations and overrides (§3.3) -------------------------------

export function conditionFixture(overrides: Partial<Condition> = {}): Condition {
  return { in: "query", name: "debug", op: "equals", value: "1", ...overrides };
}

export function recipeFixture(overrides: Partial<Recipe> = {}): Recipe {
  return {
    kind: "jwt",
    value: undefined,
    field: "token",
    offset: "",
    format: "",
    claims: { sub: "1" },
    ttlSec: 3600,
    ...overrides,
  };
}

export function variantFixture(overrides: Partial<Variant> = {}): Variant {
  return {
    mode: "generated",
    when: [],
    body: undefined,
    bodyEncoding: "",
    mediaType: "application/json",
    headers: {},
    schemaPatch: undefined,
    recipes: {},
    ...overrides,
  };
}

export function mergedStatusViewFixture(
  overrides: Partial<MergedStatusView> = {},
): MergedStatusView {
  return {
    selector: "200",
    httpStatus: 200,
    isDefault: true,
    mediaType: "application/json",
    ...overrides,
  };
}

export function opOverrideSummaryViewFixture(
  overrides: Partial<OpOverrideSummaryView> = {},
): OpOverrideSummaryView {
  return {
    overrideOn: false,
    routeOff: false,
    activeStatus: 200,
    responses: { "200": { mode: "generated", recipeCount: 0, hasSchemaPatch: false } },
    updatedAt: 0,
    editVersion: 1,
    ...overrides,
  };
}

export function mergedOperationViewFixture(
  overrides: Partial<MergedOperationView> = {},
): MergedOperationView {
  return {
    method: "GET",
    path: "/pets/{petId}",
    // Already percent-encoded, matching what the server actually hands the
    // client (§3.3): "GET /pets/{petId}" through url.PathEscape.
    opKey: "GET%20%2Fpets%2F%7BpetId%7D",
    statuses: [mergedStatusViewFixture()],
    override: opOverrideSummaryViewFixture(),
    ...overrides,
  };
}

export function overrideDocViewFixture(overrides: Partial<OverrideDocView> = {}): OverrideDocView {
  return {
    method: "GET",
    path: "/pets/{petId}",
    opKey: "GET%20%2Fpets%2F%7BpetId%7D",
    updatedAt: 0,
    overrideOn: false,
    routeOff: false,
    activeStatus: 200,
    responses: { "200": variantFixture() },
    listSize: { min: 1, max: 5 },
    delayMs: 0,
    failDirective: undefined,
    validateReq: false,
    editVersion: 1,
    ...overrides,
  };
}

// --- Session / live state (§3.4) ------------------------------------------

export function directiveFixture(overrides: Partial<Directive> = {}): Directive {
  return {
    target: "*",
    action: "status",
    status: 500,
    once: false,
    n: 0,
    setAt: "2026-08-19T00:00:00Z",
    scenario: undefined,
    ...overrides,
  };
}

export function sessionListViewFixture(overrides: Partial<SessionListView> = {}): SessionListView {
  return { directives: [directiveFixture()], ...overrides };
}

// --- Traffic (§3.5) --------------------------------------------------------

export function trafficRowFixture(overrides: Partial<TrafficRow> = {}): TrafficRow {
  return {
    id: 1,
    ts: "2026-08-19T00:00:00Z",
    method: "GET",
    path: "/pets/1",
    peerIp: "127.0.0.1",
    fwdIp: "",
    matchedKind: "operation",
    matchedId: 1,
    status: 200,
    durationMs: 5,
    reqHeaders: {},
    reqBody: "",
    respBody: "{}",
    notes: "",
    truncated: false,
    ...overrides,
  };
}

export function trafficListViewFixture(overrides: Partial<TrafficListView> = {}): TrafficListView {
  return { rows: [trafficRowFixture()], rate1m: 0, dropped: 0, ...overrides };
}

export function trafficPollViewFixture(overrides: Partial<TrafficPollView> = {}): TrafficPollView {
  return { rows: [trafficRowFixture()], lastId: 1, dropped: 0, ...overrides };
}

// --- Custom endpoints (§3.6) ------------------------------------------------

export function endpointViewFixture(overrides: Partial<EndpointView> = {}): EndpointView {
  return {
    id: 1,
    method: "GET",
    path: "/custom/ping",
    canonicalPath: "/custom/ping",
    overrideOn: true,
    routeOff: false,
    activeStatus: 200,
    responses: { "200": variantFixture() },
    listSize: { min: 1, max: 5 },
    delayMs: 0,
    kind: "http",
    createdAt: 0,
    updatedAt: 0,
    editVersion: 1,
    ...overrides,
  };
}

export function endpointListViewFixture(
  overrides: Partial<EndpointListView> = {},
): EndpointListView {
  return { endpoints: [endpointViewFixture()], ...overrides };
}

// --- Auth preset (§3.7) -----------------------------------------------------

export function bindingFixture(overrides: Partial<Binding> = {}): Binding {
  return {
    method: "POST",
    path: "/auth/login",
    status: 200,
    dataPath: "token",
    recipe: recipeFixture(),
    reason: "matches a known auth path",
    source: "auth-path",
    ...overrides,
  };
}

export function authPresetProposalFixture(
  overrides: Partial<AuthPresetProposal> = {},
): AuthPresetProposal {
  return {
    bindings: [bindingFixture()],
    schemes: ["bearerAuth: http bearer"],
    authPaths: ["/auth/login"],
    notes: [],
    sampleJwt: "eyJhbGciOiJIUzI1NiJ9.e30.signature",
    // A3/D5: the opKey-keyed edit_version map the panel must send back
    // unchanged through ApplyAuthPresetRequest.editVersions (D12) — GET
    // .../auth-preset is the sixth read that grows the token, and this is
    // its fixture's share of that.
    editVersions: { "POST%20%2Fauth%2Flogin": 1 },
    ...overrides,
  };
}
