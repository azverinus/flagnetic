# Changelog

## [0.2.0](https://github.com/azverinus/flagnetic/compare/sdks/ruby/v0.1.3...sdks/ruby/v0.2.0) (2026-11-22)


### ⚠ BREAKING CHANGES

* **sdk-ruby:** freeze the 1.0 API, log deprecations ([#426](https://github.com/azverinus/flagnetic/issues/426))
* **sdks:** pin delivery in conformance, one set of log names ([#423](https://github.com/azverinus/flagnetic/issues/423))

### Features

* **sdk-ruby:** freeze the 1.0 API, log deprecations ([#426](https://github.com/azverinus/flagnetic/issues/426)) ([7fe7546](https://github.com/azverinus/flagnetic/commit/7fe754637b690d82e35c5866ed6135aacaeb80b9))
* **sdks:** pin delivery in conformance, one set of log names ([#423](https://github.com/azverinus/flagnetic/issues/423)) ([8eb53b9](https://github.com/azverinus/flagnetic/commit/8eb53b97e031010059fe586ebc3ec5cf9cc246de))


### Bug Fixes

* **eval:** close the evaluator and conformance findings ([#393](https://github.com/azverinus/flagnetic/issues/393)) ([7005803](https://github.com/azverinus/flagnetic/commit/70058031748337765046b86c8af7ab7e149258e8))
* **sdks:** close the Python and Ruby SDK review findings ([#414](https://github.com/azverinus/flagnetic/issues/414)) ([d02c998](https://github.com/azverinus/flagnetic/commit/d02c998264c98f3a0904f0187d779be749270237))

## [0.1.3](https://github.com/azverinus/flagnetic/compare/sdks/ruby/v0.1.2...sdks/ruby/v0.1.3) (2026-11-17)


### Bug Fixes

* **sdks:** bound stream lines, events, response bodies and the matched input ([#337](https://github.com/azverinus/flagnetic/issues/337)) ([695477d](https://github.com/azverinus/flagnetic/commit/695477d7d31700fe25b1458eefd315e733355b2a))
* **sdks:** harden the smaller SDK findings ([#353](https://github.com/azverinus/flagnetic/issues/353)) ([dfa56a4](https://github.com/azverinus/flagnetic/commit/dfa56a4bbbebb92c97d1f63810130c0ec2c45b0b))

## [0.1.2](https://github.com/azverinus/flagnetic/compare/sdks/ruby/v0.1.1...sdks/ruby/v0.1.2) (2026-11-16)


### Bug Fixes

* **sdk-ruby:** back off from a stream closed before its first heartbeat ([#319](https://github.com/azverinus/flagnetic/issues/319)) ([51824cb](https://github.com/azverinus/flagnetic/commit/51824cb66f2e4aa41f1d15f4864935844a94a664))
* **sdk-ruby:** bind the store file to the key it was received under ([#315](https://github.com/azverinus/flagnetic/issues/315)) ([56a2b99](https://github.com/azverinus/flagnetic/commit/56a2b99d010a4a3c6da6af938ac2512682d09c74))
* **sdk-ruby:** match regular expressions in linear time ([#281](https://github.com/azverinus/flagnetic/issues/281)) ([b69c8f0](https://github.com/azverinus/flagnetic/commit/b69c8f0cbe03f3e2e6997c0fdace18658e1120d3))
* **sdk-ruby:** read Unicode class names as the server does ([#310](https://github.com/azverinus/flagnetic/issues/310)) ([b1e3fa5](https://github.com/azverinus/flagnetic/commit/b1e3fa5781ff497d8cefced0b98f468aedcada83))
* **sdk-ruby:** send a string that is no UTF-8 scrubbed, drop an unencodable event alone ([#307](https://github.com/azverinus/flagnetic/issues/307)) ([da7fe3f](https://github.com/azverinus/flagnetic/commit/da7fe3f36fde5fdaa649a45e4fe1eb9755d42540))

## [0.1.1](https://github.com/azverinus/flagnetic/compare/sdks/ruby/v0.1.0...sdks/ruby/v0.1.1) (2026-11-11)


### Bug Fixes

* **sdk-ruby:** keep the worker thread alive through any exception ([#242](https://github.com/azverinus/flagnetic/issues/242)) ([6036682](https://github.com/azverinus/flagnetic/commit/6036682ca28d74335707bc13cb5f9ae0a0e6842c))

## 0.1.0 (2026-10-28)


### Features

* **sdk-js:** release the npm package and prove it against two replicas ([#134](https://github.com/azverinus/flagnetic/issues/134)) ([004bfa1](https://github.com/azverinus/flagnetic/commit/004bfa118763baea6d8758db0de909990aafe6ed))
* **sdk-ruby:** configure Flagnetic.client in Rails without an initializer ([#108](https://github.com/azverinus/flagnetic/issues/108)) ([eba820b](https://github.com/azverinus/flagnetic/commit/eba820b457630d5c111fe2c7a6d18fc36170b7cf))
* **sdk-ruby:** evaluate any ruleset as the reference evaluator does ([#70](https://github.com/azverinus/flagnetic/issues/70)) ([0b0e915](https://github.com/azverinus/flagnetic/commit/0b0e915dc819686fe5449d890a54950049f3ca39))
* **sdk-ruby:** hold a fresh ruleset with a server key ([#82](https://github.com/azverinus/flagnetic/issues/82)) ([f409d19](https://github.com/azverinus/flagnetic/commit/f409d1900e93266b4c525f40cbbe294a63557f31))
* **sdk-ruby:** release both gems through RubyGems trusted publishing ([#150](https://github.com/azverinus/flagnetic/issues/150)) ([b84f3eb](https://github.com/azverinus/flagnetic/commit/b84f3eb25bb36cbae791720cd3087986cce5b541))
* **sdk-ruby:** report events and add an offline test helper ([#92](https://github.com/azverinus/flagnetic/issues/92)) ([9fbfc65](https://github.com/azverinus/flagnetic/commit/9fbfc658acfbb026ae8150b2f364d9dcefc8b690))
