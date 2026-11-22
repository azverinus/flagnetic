/// Knobs for tests of an app that uses Flagnetic: the timings, jitter,
/// randomness and clock of a client, replaced so that a scenario runs in
/// milliseconds.
///
/// This library is not part of the API the package promises from 1.0: its
/// contents may change in any release. Everything an app configures is in
/// `package:flagnetic/flagnetic.dart`.
library;

export 'src/client/testing_options.dart' show TestingOptions;
