/// Flutter bindings for Flagnetic: a scope holding the client, a builder
/// widget rebuilding when a flag changes, listenables for a flag and for the
/// fallback chain's stage, the lifecycle observer moving the client between
/// foreground and background, the default store and the Dev Tools helper
/// and screen for local overrides.
///
/// The client, the fallback chain and offline mode live in
/// `package:flagnetic/flagnetic.dart`, which this package re-exports: one
/// import is enough in an app.
///
/// ```dart
/// final store = FlutterStore();
/// final client = Flagnetic.client(
///   'fn_cli_…',
///   baseUrl: Uri.parse('https://flags.example.com'),
///   context: FlagneticContext(units: {'anonymous': await anonymousUnitId(store, 'anonymous')}),
///   options: ClientOptions(store: store, platform: 'android'),
/// );
/// runApp(FlagneticScope(client: client, child: const MyApp()));
/// ```
library;

export 'package:flagnetic/flagnetic.dart';

export 'src/dev_tools.dart' show FlagneticDevTools;
export 'src/dev_tools_screen.dart' show FlagneticDevToolsScreen;
export 'src/flag_builder.dart' show FlagBuilder;
export 'src/lifecycle.dart' show FlagneticLifecycleObserver;
export 'src/listenable.dart' show FlagListenable, StageListenable;
export 'src/scope.dart' show FlagneticScope;
export 'src/store.dart' show FlutterStore;
