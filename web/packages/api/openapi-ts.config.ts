import { defineConfig } from "@hey-api/openapi-ts";

/**
 * Generates the TypeScript client and the zod request and response schemas
 * of the management API from its OpenAPI document. The output is committed,
 * so CI fails when the document changes and this is not regenerated.
 */
export default defineConfig({
  input: "../../../api/management.yaml",
  output: {
    path: "src/generated",
    postProcess: ["prettier"],
  },
  plugins: [
    { name: "@hey-api/client-fetch", bundle: true },
    { name: "@hey-api/typescript" },
    { name: "@hey-api/sdk", client: true },
    { name: "zod", requests: true, responses: true },
  ],
});
