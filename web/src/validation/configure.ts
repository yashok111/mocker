import { configure } from "arktype/config";

// Loaded before any schemas: the admin CSP forbids dynamic code generation.
// The @ark/schema Yarn patch also skips its CSP probe when this is explicit.
configure({ jitless: true });
