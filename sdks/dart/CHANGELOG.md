# Changelog

## [0.2.0](https://github.com/azverinus/flagnetic/compare/sdks/dart/v0.1.4...sdks/dart/v0.2.0) (2026-11-22)


### ⚠ BREAKING CHANGES

* **sdks:** one operation_deprecated log name in every SDK ([#430](https://github.com/azverinus/flagnetic/issues/430))
* **sdk-dart:** freeze the 1.0 API, log deprecations, add a quickstart ([#428](https://github.com/azverinus/flagnetic/issues/428))
* **sdks:** pin delivery in conformance, one set of log names ([#423](https://github.com/azverinus/flagnetic/issues/423))

### Features

* **sdk-dart:** freeze the 1.0 API, log deprecations, add a quickstart ([#428](https://github.com/azverinus/flagnetic/issues/428)) ([dc6facb](https://github.com/azverinus/flagnetic/commit/dc6facb7f0a7df490bbaa3d8630a0a6a0472e52a))
* **sdks:** one operation_deprecated log name in every SDK ([#430](https://github.com/azverinus/flagnetic/issues/430)) ([f8a28d9](https://github.com/azverinus/flagnetic/commit/f8a28d9640a2edafc0bc4e45409eda22168023a8))
* **sdks:** pin delivery in conformance, one set of log names ([#423](https://github.com/azverinus/flagnetic/issues/423)) ([8eb53b9](https://github.com/azverinus/flagnetic/commit/8eb53b97e031010059fe586ebc3ec5cf9cc246de))


### Bug Fixes

* **sdks:** close the Dart and Flutter review findings ([#417](https://github.com/azverinus/flagnetic/issues/417)) ([0ef349e](https://github.com/azverinus/flagnetic/commit/0ef349ea9beab8bbd6a1fe54138382aafa3c15f9))
* **sdks:** close the JavaScript review findings, add summaryInterval ([#413](https://github.com/azverinus/flagnetic/issues/413)) ([f018603](https://github.com/azverinus/flagnetic/commit/f018603be9a9876438b2490c7bc4efefd23fbe71))

## [0.1.4](https://github.com/azverinus/flagnetic/compare/sdks/dart/v0.1.3...sdks/dart/v0.1.4) (2026-11-17)


### Bug Fixes

* **delivery:** narrow what a client key learns from answers and refusals ([#346](https://github.com/azverinus/flagnetic/issues/346)) ([f29ee44](https://github.com/azverinus/flagnetic/commit/f29ee44b01c8fdeafbcb9342ef062afdc6dd2453))
* **sdks:** bound stream lines, events, response bodies and the matched input ([#337](https://github.com/azverinus/flagnetic/issues/337)) ([695477d](https://github.com/azverinus/flagnetic/commit/695477d7d31700fe25b1458eefd315e733355b2a))
* **sdks:** harden the smaller SDK findings ([#353](https://github.com/azverinus/flagnetic/issues/353)) ([dfa56a4](https://github.com/azverinus/flagnetic/commit/dfa56a4bbbebb92c97d1f63810130c0ec2c45b0b))
* **sdks:** record no exposure for the previous context's values ([#345](https://github.com/azverinus/flagnetic/issues/345)) ([b2a0899](https://github.com/azverinus/flagnetic/commit/b2a089971ebeaac8429b8b603dd519787fa62825))

## [0.1.3](https://github.com/azverinus/flagnetic/compare/sdks/dart/v0.1.2...sdks/dart/v0.1.3) (2026-11-16)


### Bug Fixes

* **sdk-dart:** back off from a stream closed before its first heartbeat ([#320](https://github.com/azverinus/flagnetic/issues/320)) ([7ee2316](https://github.com/azverinus/flagnetic/commit/7ee2316d3384b0a6c886e4a9db6789f2f5b8ef01))
* **sdk-dart:** fire on a changed served flag, wholeness by static type ([#280](https://github.com/azverinus/flagnetic/issues/280)) ([9f52f44](https://github.com/azverinus/flagnetic/commit/9f52f44c76344ceeaeb3a3e1a75513f6a8783e7c))

## [0.1.2](https://github.com/azverinus/flagnetic/compare/sdks/dart/v0.1.1...sdks/dart/v0.1.2) (2026-11-12)


### Bug Fixes

* **sdk-dart:** refuse an SDK key that is no header token and never log it ([#265](https://github.com/azverinus/flagnetic/issues/265)) ([3ab14c9](https://github.com/azverinus/flagnetic/commit/3ab14c9859f7527eaf40fcfa6675e94c77bc84d4))

## [0.1.1](https://github.com/azverinus/flagnetic/compare/sdks/dart/v0.1.0...sdks/dart/v0.1.1) (2026-11-11)


### Bug Fixes

* **sdk-dart:** cancel a timed-out request and bound response bodies ([#253](https://github.com/azverinus/flagnetic/issues/253)) ([43fb2ec](https://github.com/azverinus/flagnetic/commit/43fb2ecd7aaf56103782aec023c42743117eb240))

## 0.1.0 (2026-10-28)


### Features

* **sdk-dart:** add the Dart client mode ([#74](https://github.com/azverinus/flagnetic/issues/74)) ([2e8d8b7](https://github.com/azverinus/flagnetic/commit/2e8d8b7bbf603ec99e4bce5db05c17639add3782))
* **sdk-dart:** hold the stream in the foreground ([#88](https://github.com/azverinus/flagnetic/issues/88)) ([71e78ae](https://github.com/azverinus/flagnetic/commit/71e78aea3b860003cd3b11cefd31d72c56ebbc20))
* **sdk-dart:** release the pub.dev packages and prove them against two replicas ([#151](https://github.com/azverinus/flagnetic/issues/151)) ([edc4871](https://github.com/azverinus/flagnetic/commit/edc48713a8926cd6923cc535d5488a6a4af78016))
* **sdk-dart:** serve Dart apps compiled for the web ([#93](https://github.com/azverinus/flagnetic/issues/93)) ([71c0c86](https://github.com/azverinus/flagnetic/commit/71c0c863ab72d3b15759611d1800381c0e86f6f1))
* **sdk-flutter:** add flagnetic_flutter with widgets, lifecycle, store and Dev Tools ([#102](https://github.com/azverinus/flagnetic/issues/102)) ([57d4805](https://github.com/azverinus/flagnetic/commit/57d480524f5c421f632456da1f1b731c76f2b706))
