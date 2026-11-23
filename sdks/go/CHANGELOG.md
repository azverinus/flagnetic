# Changelog

## [0.2.0](https://github.com/azverinus/flagnetic/compare/sdks/go/v0.1.2...sdks/go/v0.2.0) (2026-11-23)


### ⚠ BREAKING CHANGES

* **sdks:** one operation_deprecated log name in every SDK ([#430](https://github.com/azverinus/flagnetic/issues/430))
* **sdk-go:** freeze the Go API, log deprecations, add a quickstart ([#427](https://github.com/azverinus/flagnetic/issues/427))
* **sdks:** pin delivery in conformance, one set of log names ([#423](https://github.com/azverinus/flagnetic/issues/423))

### Features

* **sdk-go:** freeze the Go API, log deprecations, add a quickstart ([#427](https://github.com/azverinus/flagnetic/issues/427)) ([8a18e41](https://github.com/azverinus/flagnetic/commit/8a18e412a718588bde884cf0570f9ea95fca1536))
* **sdks:** one operation_deprecated log name in every SDK ([#430](https://github.com/azverinus/flagnetic/issues/430)) ([f8a28d9](https://github.com/azverinus/flagnetic/commit/f8a28d9640a2edafc0bc4e45409eda22168023a8))
* **sdks:** pin delivery in conformance, one set of log names ([#423](https://github.com/azverinus/flagnetic/issues/423)) ([8eb53b9](https://github.com/azverinus/flagnetic/commit/8eb53b97e031010059fe586ebc3ec5cf9cc246de))


### Bug Fixes

* **sdk-go:** require eval 0.2.0 ([#433](https://github.com/azverinus/flagnetic/issues/433)) ([6f8c87b](https://github.com/azverinus/flagnetic/commit/6f8c87bde4536ae63e5eeb0dbb804e8dc78a1aed))
* **sdks:** close the Go SDK and provider review findings ([#406](https://github.com/azverinus/flagnetic/issues/406)) ([3d12bd8](https://github.com/azverinus/flagnetic/commit/3d12bd8eb37c8daa4547ef381512d16aedca243b))

## [0.1.2](https://github.com/azverinus/flagnetic/compare/sdks/go/v0.1.1...sdks/go/v0.1.2) (2026-11-17)


### Bug Fixes

* **delivery:** narrow what a client key learns from answers and refusals ([#346](https://github.com/azverinus/flagnetic/issues/346)) ([f29ee44](https://github.com/azverinus/flagnetic/commit/f29ee44b01c8fdeafbcb9342ef062afdc6dd2453))
* **sdks:** bound stream lines, events, response bodies and the matched input ([#337](https://github.com/azverinus/flagnetic/issues/337)) ([695477d](https://github.com/azverinus/flagnetic/commit/695477d7d31700fe25b1458eefd315e733355b2a))
* **sdks:** harden the smaller SDK findings ([#353](https://github.com/azverinus/flagnetic/issues/353)) ([dfa56a4](https://github.com/azverinus/flagnetic/commit/dfa56a4bbbebb92c97d1f63810130c0ec2c45b0b))

## [0.1.1](https://github.com/azverinus/flagnetic/compare/sdks/go/v0.1.0...sdks/go/v0.1.1) (2026-11-16)


### Bug Fixes

* **sdk-go:** back off from a stream closed before its first heartbeat ([#316](https://github.com/azverinus/flagnetic/issues/316)) ([b5f62c3](https://github.com/azverinus/flagnetic/commit/b5f62c3e8b4f7dc48ab33d8d4b430a577884e4f6))
* **sdk-go:** bind the store file to the key it was received under ([#306](https://github.com/azverinus/flagnetic/issues/306)) ([09565f3](https://github.com/azverinus/flagnetic/commit/09565f31a1eecbaeb8c5dc1b7abfc4486e42e7ca))

## 0.1.0 (2026-10-23)


### Features

* **delivery:** add the SDK contract, one ruleset scope, clock versions ([#43](https://github.com/azverinus/flagnetic/issues/43)) ([f2e84f3](https://github.com/azverinus/flagnetic/commit/f2e84f3df862a41421e6e30ef78d0b36fb72a7b6))
* **sdk-go:** add flagnetictest, the OpenFeature provider and module releases ([#16](https://github.com/azverinus/flagnetic/issues/16)) ([2fc4944](https://github.com/azverinus/flagnetic/commit/2fc49442d7552fa1e332bb9665e587c6b1b69fce))
* **sdk-go:** add the Go SDK with its protocol conformance suite ([#15](https://github.com/azverinus/flagnetic/issues/15)) ([ffde478](https://github.com/azverinus/flagnetic/commit/ffde478bffd0a4ff71e8b079e44c84b51fba5d9b))


### Dependencies

* **sdk-go:** require eval v0.1.0 ([#62](https://github.com/azverinus/flagnetic/issues/62)) ([89e90fe](https://github.com/azverinus/flagnetic/commit/89e90fee9f3de6a658dd524c2eec55a1accd2781))
