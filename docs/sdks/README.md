# SDKs

Every Flagnetic SDK is in its 0.x line and speaks the same protocol: `/sdk/v1`, ruleset `schema_version` 1, server key or client key by mode. Pick the one for your language; each page covers its packages, runtimes, options and how soon a change reaches it.

| Language | Install | Mode | Key | Runtimes | A change reaches it within |
|---|---|---|---|---|---|
| [Go](../../sdks/go/README.md) | `go get github.com/azverinus/flagnetic/sdks/go` | server | server | Go 1.26 or newer | 2 s at p99 streaming, 30 s polling |
| [Go, OpenFeature](../../sdks/go-openfeature/README.md) | `go get github.com/azverinus/flagnetic/sdks/go-openfeature` | server | server | as the Go SDK | as the Go SDK |
| [JavaScript](javascript.md) | `npm install flagnetic` | server, browser client | server (`flagnetic/server`), client (`flagnetic/client`, `flagnetic/react`) | Node 22 and later; browsers that run ES2022; React 18 and later | server: 2 s at p99 streaming, 30 s polling; browser: 300 s, at once when the tab is shown again |
| [Ruby](ruby.md) | `bundle add flagnetic` | server | server | Ruby ≥ 3.3; Rails ≥ 8.0 for the integration | 2 s at p99 streaming, 30 s polling |
| [Ruby, OpenFeature](ruby.md) | `bundle add flagnetic-openfeature` | server | server | Ruby ≥ 3.4 | as `flagnetic` |
| [Python](python.md) | `pip install flagnetic` (`'flagnetic[openfeature]'` for OpenFeature) | server | server | Python ≥ 3.11 | 2 s at p99 streaming, 30 s polling |
| [Dart](dart.md) | `dart pub add flagnetic` | client, on the Dart VM and the web | client | Dart ≥ 3.9 | foreground: 5 s at p99; background: at the next resume; web: 5 min and when the page is shown again |
| [Flutter](dart.md) | `flutter pub add flagnetic_flutter` | client | client | Flutter ≥ 3.35 on iOS, Android, desktop and the web | as Dart |

A **server** SDK downloads the environment's whole ruleset and evaluates in the process, with the true reason; keep its key in a backend. A **client** SDK posts its context to the server and learns served values only; its key ships inside the app. See [SDK keys](keys.md) and [what a key learns](compatibility.md#what-a-key-learns).

OpenFeature providers exist for Go, JavaScript (Node and web), Ruby and Python. Dart and Flutter have none yet.

## Compatibility

Every SDK works with every Flagnetic server from 0.1.0 on, and the protocol between them only grows; see [Versioning and compatibility](compatibility.md) for the table and the rules a build follows when it meets a document it cannot read. [Behaviour matrix](behaviour-matrix.md) shows which protocol scenario each SDK passes, built from each SDK's own test run.

## Writing another SDK

[Implementing an SDK](conformance.md) lists the conformance suite in the order an author works through it.
