/**
 * `flagnetic/openfeature`: OpenFeature providers. `FlagneticServerProvider`
 * serves `@openfeature/server-sdk` in Node over a server-mode client;
 * `FlagneticWebProvider` serves `@openfeature/web-sdk` in the browser over a
 * browser client. Both SDKs are optional peer dependencies, and this entry
 * point loads neither at run time: install the one your side uses.
 */
export { FlagneticServerProvider, type FlagneticServerProviderOptions } from "./openfeature/server-provider.js";
export { FlagneticWebProvider, type FlagneticWebProviderOptions } from "./openfeature/web-provider.js";
export { reasonOf, REASON_METADATA_KEY } from "./openfeature/mapping.js";
