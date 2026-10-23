/// Flagnetic feature flags and experiments for Dart apps: a client that
/// posts a context to remote evaluation with a client key and serves every
/// flag's value from memory through the fallback chain: fresh values, else
/// the last good values from the store, else built-in defaults.
///
/// ```dart
/// final client = Flagnetic.client(
///   'fn_cli_…',
///   baseUrl: Uri.parse('https://flags.example.com'),
///   context: const FlagneticContext(units: {'anonymous': 'd-8f1c'}),
///   options: const ClientOptions(platform: 'android', appVersion: '1.42.0'),
/// );
/// final checkout = client.getBool('checkout', false); // synchronous
/// await client.ready(const Duration(seconds: 2));
/// ```
///
/// There is no evaluator in this package: a client key never receives a
/// ruleset, only the values the platform evaluated for its context.
library;

export 'src/client/anonymous.dart' show anonymousUnitId, anonymousUnitIdKey;
export 'src/client/client.dart'
    show ChangeListener, Flagnetic, FlagneticClient, OfflineMode, StageListener;
export 'src/client/context.dart' show FlagneticContext;
export 'src/client/events.dart' show SummaryRow;
export 'src/client/options.dart' show ClientOptions, OfflineSource;
export 'src/client/overrides.dart' show LocalOverrides;
export 'src/client/store.dart'
    show FlagneticStore, InMemoryStore, StoreKeys, isLastGoodValuesKey;
export 'src/client/types.dart'
    show
        Detail,
        Experiment,
        FallbackCodes,
        FlagSummary,
        LogEvents,
        LogLevel,
        Logger,
        Reasons,
        Stage,
        Status;
