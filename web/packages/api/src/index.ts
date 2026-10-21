/**
 * The management API as the console sees it: the client generated from
 * `api/management.yaml`, its types and the zod schemas of its requests and
 * responses. Nothing in the console describes the management API by hand.
 */
export * from "./generated/index.js";
export * from "./generated/zod.gen.js";
export { client } from "./generated/client.gen.js";
export { createClient, createConfig } from "./generated/client/index.js";
export type { Client, Config, RequestResult } from "./generated/client/index.js";
