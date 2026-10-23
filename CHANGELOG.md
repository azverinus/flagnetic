# Changelog

## 0.1.0 (2026-10-23)


### Features

* **access:** add personal access tokens, grants and project tokens ([#29](https://github.com/azverinus/flagnetic/issues/29)) ([a8b426e](https://github.com/azverinus/flagnetic/commit/a8b426eab26419284e36576715b28bcad51693d1))
* **access:** manage instance members and invite them with a one-time link ([#26](https://github.com/azverinus/flagnetic/issues/26)) ([65fe644](https://github.com/azverinus/flagnetic/commit/65fe644daf406ee45515e7c6dd76fa1c369f3b2e))
* **access:** manage sessions, purge auth leftovers and edit auth settings at runtime ([#25](https://github.com/azverinus/flagnetic/issues/25)) ([2b2b3a7](https://github.com/azverinus/flagnetic/commit/2b2b3a7b8d1ba44884f159ed3b6ec1c465cea5ad))
* **access:** open the management API with bootstrap and the secret key ([#18](https://github.com/azverinus/flagnetic/issues/18)) ([a6a6a02](https://github.com/azverinus/flagnetic/commit/a6a6a02c277a813fadc9b68438bbe3f8822d9dc4))
* **access:** sign in to sessions with lockout, throttling and a hashing bound ([#24](https://github.com/azverinus/flagnetic/issues/24)) ([dad2d1c](https://github.com/azverinus/flagnetic/commit/dad2d1c72cb7a51275bfd6695ccf905f5fd0325a))
* **console:** add instance members, auth settings and invitations ([#33](https://github.com/azverinus/flagnetic/issues/33)) ([f22c068](https://github.com/azverinus/flagnetic/commit/f22c068de06ac83254cccd17a005532f1cdfe7ac))
* **console:** add personal settings for profile, password and tokens ([#31](https://github.com/azverinus/flagnetic/issues/31)) ([5367590](https://github.com/azverinus/flagnetic/commit/536759094bec824d6dcc924c333b1abba8cd4f94))
* **console:** add project members, grants and project tokens ([#34](https://github.com/azverinus/flagnetic/issues/34)) ([75a3497](https://github.com/azverinus/flagnetic/commit/75a3497e25a0e9e4b4229b8e51d94424f717baab))
* **console:** add project screens, access in the UI and the audit logs ([#30](https://github.com/azverinus/flagnetic/issues/30)) ([431e0ad](https://github.com/azverinus/flagnetic/commit/431e0adf89e929ea18dc2a04fca89ed6f3b54e83))
* **console:** embed the console with sign-in, the shell, primitives and e2e tests ([#27](https://github.com/azverinus/flagnetic/issues/27)) ([469db3c](https://github.com/azverinus/flagnetic/commit/469db3cd1e666232c587e3d4c311338e525e1d30))
* **console:** list flags and create a flag ([#42](https://github.com/azverinus/flagnetic/issues/42)) ([4454a8f](https://github.com/azverinus/flagnetic/commit/4454a8f627fadcb36748946282815a317af5a253))
* **console:** list SDKs, download the ruleset and handle discovered attributes ([#59](https://github.com/azverinus/flagnetic/issues/59)) ([a85ca13](https://github.com/azverinus/flagnetic/commit/a85ca13288fa871d0b4fb98ec21076f882c841b7))
* **console:** list, create, edit and delete segments ([#46](https://github.com/azverinus/flagnetic/issues/46)) ([0219203](https://github.com/azverinus/flagnetic/commit/0219203d959890c1b042fc709c33f1f5ad867e6b))
* **console:** manage SDK keys from an environment page ([#52](https://github.com/azverinus/flagnetic/issues/52)) ([1c51a00](https://github.com/azverinus/flagnetic/commit/1c51a00abbcb83bf70acf408987769d1bd6bcf47))
* **console:** open a flag, edit its rules, overrides and kill switch ([#44](https://github.com/azverinus/flagnetic/issues/44)) ([36a6494](https://github.com/azverinus/flagnetic/commit/36a6494493ade92e0d42ad3c494d7c6e32e20b98))
* **console:** resolve workspace conflicts and show usage counts ([#48](https://github.com/azverinus/flagnetic/issues/48)) ([9458d7c](https://github.com/azverinus/flagnetic/commit/9458d7c11ed0967f52a140655e40c576d97fef9a))
* **console:** show flag history, compare environments and test a flag ([#47](https://github.com/azverinus/flagnetic/issues/47)) ([5f76b8e](https://github.com/azverinus/flagnetic/commit/5f76b8e8d46a8513f475c6624506281a700b5043))
* **delivery:** add the SDK contract, one ruleset scope, clock versions ([#43](https://github.com/azverinus/flagnetic/issues/43)) ([f2e84f3](https://github.com/azverinus/flagnetic/commit/f2e84f3df862a41421e6e30ef78d0b36fb72a7b6))
* **delivery:** evaluate remotely, allow browser origins, discover attributes ([#51](https://github.com/azverinus/flagnetic/issues/51)) ([8a8e5a0](https://github.com/azverinus/flagnetic/commit/8a8e5a0d20d00c455a7d257d483f4d1611d728fc))
* **delivery:** ingest SDK events, count remote evaluations, list SDKs ([#53](https://github.com/azverinus/flagnetic/issues/53)) ([9b0301c](https://github.com/azverinus/flagnetic/commit/9b0301c1ca2a35de6d37df41f0fb5942ef30ccac))
* **delivery:** manage SDK keys per environment ([#45](https://github.com/azverinus/flagnetic/issues/45)) ([386e9c2](https://github.com/azverinus/flagnetic/commit/386e9c282056730e13914f1c64f86e82f0fdd99f))
* **delivery:** name SDKs without a User-Agent and take beacon events ([#54](https://github.com/azverinus/flagnetic/issues/54)) ([a72fccd](https://github.com/azverinus/flagnetic/commit/a72fccdcc13ca8f28400fefeb99ffcc219febf74))
* **delivery:** serve the ruleset under /sdk/v1 with a per-replica cache ([#49](https://github.com/azverinus/flagnetic/issues/49)) ([60b503b](https://github.com/azverinus/flagnetic/commit/60b503b0fc82683f3edcdeabfb049226790690d0))
* **delivery:** stream ruleset versions and rate-limit the SDK API ([#50](https://github.com/azverinus/flagnetic/issues/50)) ([99b236e](https://github.com/azverinus/flagnetic/commit/99b236ee35260d8e54e410c4e64ad7d2e1626fb4))
* **eval:** add the evaluator module and the conformance suite ([#11](https://github.com/azverinus/flagnetic/issues/11)) ([476afe7](https://github.com/azverinus/flagnetic/commit/476afe75d60f71b3c7d7d21ae1c7f0f9158e08aa))
* **flags:** add flag definitions, archival and segments ([#32](https://github.com/azverinus/flagnetic/issues/32)) ([1239b79](https://github.com/azverinus/flagnetic/commit/1239b79ca5530e6386684e7697f48f3ded063f48))
* **flags:** guard referenced declarations and filter the flags list ([#36](https://github.com/azverinus/flagnetic/issues/36)) ([3d3f01c](https://github.com/azverinus/flagnetic/commit/3d3f01cb163c1713103832c8a3d2f66bb24bc474))
* **flags:** materialize rulesets, evaluate, revert and copy configurations ([#38](https://github.com/azverinus/flagnetic/issues/38)) ([93363c8](https://github.com/azverinus/flagnetic/commit/93363c86c9461ab354db303f3e1aa8028e3d17f4))
* **flags:** replace flag configurations, set overrides and kill flags ([#35](https://github.com/azverinus/flagnetic/issues/35)) ([621d2fe](https://github.com/azverinus/flagnetic/commit/621d2fef30c78900e49e8448e53289d057acbf24))
* **platform:** add flagnetic serve and the transactor ([#6](https://github.com/azverinus/flagnetic/issues/6)) ([7847fd1](https://github.com/azverinus/flagnetic/commit/7847fd19d97dda93978d5985cddab8fa2d61b2a0))
* **platform:** add the kernel, the test kit and flagnetic migrate ([#5](https://github.com/azverinus/flagnetic/issues/5)) ([4785172](https://github.com/azverinus/flagnetic/commit/4785172d4f65a6c54940e151296c21a6d1f22fc5))
* **platform:** drain on stop, check the schema and answer errors as problems ([#8](https://github.com/azverinus/flagnetic/issues/8)) ([1ece833](https://github.com/azverinus/flagnetic/commit/1ece833c01d01c9b42b8872bb968a93e7b64a958))
* **platform:** resolve clients behind proxies, bound requests and serve metrics ([#9](https://github.com/azverinus/flagnetic/issues/9)) ([486600b](https://github.com/azverinus/flagnetic/commit/486600b9151e767c5b3e28388c05c5705ca3293c))
* **platform:** run workers under a recovering runner and listen for notifications ([#10](https://github.com/azverinus/flagnetic/issues/10)) ([4156045](https://github.com/azverinus/flagnetic/commit/4156045f2606e4e40b01c3eca4d490c903dda2ca))
* **sdk-go:** add flagnetictest, the OpenFeature provider and module releases ([#16](https://github.com/azverinus/flagnetic/issues/16)) ([2fc4944](https://github.com/azverinus/flagnetic/commit/2fc49442d7552fa1e332bb9665e587c6b1b69fce))
* **sdk-go:** add the Go SDK with its protocol conformance suite ([#15](https://github.com/azverinus/flagnetic/issues/15)) ([ffde478](https://github.com/azverinus/flagnetic/commit/ffde478bffd0a4ff71e8b079e44c84b51fba5d9b))
* **workspace:** add projects, environments, declarations and the audit log ([#19](https://github.com/azverinus/flagnetic/issues/19)) ([7ba23a5](https://github.com/azverinus/flagnetic/commit/7ba23a5ed0bff084121d119985c05d9aba040567))
* **workspace:** version workspace entities and document flags ([#39](https://github.com/azverinus/flagnetic/issues/39)) ([2990967](https://github.com/azverinus/flagnetic/commit/2990967cdf4ed844009a8266fdc7ef8eac35d438))


### Bug Fixes

* **console:** show a token as expired when its timer fires early ([#40](https://github.com/azverinus/flagnetic/issues/40)) ([3115518](https://github.com/azverinus/flagnetic/commit/311551856b41ebed6705b203df6ab369399d7fb2))
* **db:** hold stored hashes to SHA-256 and name indexes alike ([#61](https://github.com/azverinus/flagnetic/issues/61)) ([6f93c9c](https://github.com/azverinus/flagnetic/commit/6f93c9cd41c0d5578a6935600aba9339cd3a7f2b))
* **platform:** build with go1.26.10 for the crypto/x509 fix ([#17](https://github.com/azverinus/flagnetic/issues/17)) ([ba2dcd4](https://github.com/azverinus/flagnetic/commit/ba2dcd4e1618c477f7e7a8a6dc83238f11dba912))
* **platform:** count a panic in a notification handler ([#14](https://github.com/azverinus/flagnetic/issues/14)) ([7dff2f0](https://github.com/azverinus/flagnetic/commit/7dff2f02a9407bd39099e62d9f1b170c351f012c))
* **platform:** treat a request the client canceled as canceled ([#41](https://github.com/azverinus/flagnetic/issues/41)) ([ebd5bc0](https://github.com/azverinus/flagnetic/commit/ebd5bc0ce4206ed6beb37d9922bc036ff68a9147))

## Changelog
