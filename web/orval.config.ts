import { defineConfig } from "orval";

// The whole API client is generated from ../api/openapi.json — the same file
// internal/admin/openapi_contract_test.go checks the Go route table against.
// That is what makes the contract load-bearing rather than decorative: a route
// that exists only on one side fails the Go test bar, and nothing here is
// hand-maintained to drift out of step with it.
export default defineConfig({
  mocker: {
    input: {
      target: "../api/openapi.json",
    },
    output: {
      mode: "tags-split",
      target: "./src/api/generated/endpoints.ts",
      schemas: "./src/api/generated/schemas",
      client: "react-query",
      httpClient: "fetch",
      clean: ["./src/api/generated"],
      indexFiles: true,
      override: {
        mutator: {
          path: "./src/api/client.ts",
          name: "customFetch",
        },
        query: {
          signal: true,
        },
      },
    },
  },
});
