# flagnetic_flutter

Flutter widgets for Flagnetic feature flags and experiments, served by a self-hosted [Flagnetic](https://github.com/azverinus/flagnetic) instance: a scope holding the client, a builder widget rebuilding when a flag changes, listenables for a flag and for the fallback chain's stage, a lifecycle observer, a store on every platform and a Dev Tools screen for local overrides. The client itself lives in [`flagnetic`](https://pub.dev/packages/flagnetic), which this package re-exports, so an app imports one package.

```bash
flutter pub add flagnetic_flutter
```

Requires Flutter 3.35 or newer, on iOS, Android, desktop and the web. Supported versions follow Flutter's stable releases of about the last twelve months; a version that leaves upstream support is dropped in a minor release.

A kill switch reaches an app in the foreground within 5 seconds at the 99th percentile, a backgrounded app when it resumes, and an app built with `ClientOptions(stream: false)` within 5 minutes and at every resume; the [`flagnetic` README](https://pub.dev/packages/flagnetic) has the details and the offline test mode.

```dart
import 'package:flagnetic_flutter/flagnetic_flutter.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final store = FlutterStore();
  final client = Flagnetic.client(
    'fn_cli_…',
    baseUrl: Uri.parse('https://flags.example.com'),
    context: FlagneticContext(
      // The project declares its unit types; the SDK invents none.
      units: {'anonymous': await anonymousUnitId(store, 'anonymous')},
    ),
    options: ClientOptions(store: store, appVersion: '1.42.0'),
  );
  runApp(FlagneticScope(client: client, child: const MyApp()));
}
```

```dart
FlagBuilder<bool>(
  flagKey: 'checkout',
  defaultValue: false,
  builder: (context, showCheckout, detail) =>
      showCheckout ? const Checkout() : const Basket(),
)
```

## What it adds

- **`FlagneticScope`** holds the client for the widgets below and, unless `observeLifecycle` is off, attaches a `FlagneticLifecycleObserver`. It does not own the client: construct it before `runApp` and close it when the app does. `FlagneticScope.of(context)` returns the client and throws a `FlutterError` when there is no scope above; `maybeOf` returns null instead.
- **`FlagBuilder<T>`** builds from a flag's value and rebuilds when it changes, reading the client of the nearest scope unless one is passed. The type of `defaultValue` names the flag type asked for: `bool` is boolean, `String` is string, `num` is number, anything else is JSON. A JSON value decodes to `List<Object?>` or `Map<String, Object?>`, so type a JSON builder `Object?`, `List<Object?>` or `Map<String, Object?>`: a served value the type cannot hold, such as an array for `List<String>` or a `null` for a non-nullable map, builds with the built-in default and error code `type_mismatch` rather than throwing in `build`. The first frame already has a value, the store's last good values or the built-in default, so a screen never waits on the network.
- **`FlagListenable<T>`** and **`StageListenable`** are `ValueListenable`s over the client's change and stage listeners. `FlagListenable.detail` says why the current value was served. Call `dispose` when they are no longer used; `FlagBuilder` does it for you.
- **`FlagneticLifecycleObserver`** calls `client.foreground()` when the app resumes and `client.background()` when it is paused, hidden or detached; `inactive` is a transient interruption (a call, the app switcher) and changes nothing. Attached while the app is already paused, hidden or detached (a client built by a background task or a push handler), it calls `client.background()` at once rather than waiting for a lifecycle change that may never come. In the foreground the client holds the ruleset stream, so a kill switch reaches an open app within seconds; in the background it holds no connection, and resuming refetches unless the last attempt is less than 10 seconds old. On the web the client already follows the page's visibility by itself, and the observer's calls change nothing more.
- **`FlutterStore`** is the default `FlagneticStore`. On Android, iOS and macOS every entry (the anonymous unit IDs, the event queue, the index of unit sets and each unit set's last good values, a whole evaluate response) is a file in a directory the platform leaves out of device backups and device-to-device transfers: Android's `noBackupFilesDir`, and a directory marked excluded from backup on iOS and macOS. An anonymous ID belongs to one device, so a restored backup never gives a new phone the old phone's ID, and two devices never count as one unit in an experiment. A file is written beside its place and renamed, so a crash never leaves half a document, and an entry an earlier version kept in shared preferences or the application support directory moves there, unchanged, the first time it is read. Windows and Linux have no such directory: the small entries live in shared preferences and each unit set's last good values in a file under the application support directory. On the web every entry lives in shared preferences. A cold start offline serves the last good values, and events recorded offline are sent on a later start. A store that throws never breaks a flag check: the client logs `store_failed` and serves its built-in defaults.

## What 1.0 promises

From 1.0 the API below changes in a breaking way only in a new major version, and is deprecated in a minor release before it goes. Everything else is internal and may change in any release.

- **Widgets and listenables**: `FlagneticScope` (`of`, `maybeOf`, `observeLifecycle`), `FlagBuilder`, `FlagListenable`, `StageListenable` and `FlagneticLifecycleObserver`.
- **Store**: `FlutterStore` and where it keeps entries (the backup-free directories described above).
- **Dev Tools**: `FlagneticDevTools` and `FlagneticDevToolsScreen`.
- **The client**: this package re-exports `flagnetic`, and the [API that package promises](https://github.com/azverinus/flagnetic/blob/main/sdks/dart/README.md#what-10-promises) is promised here too. `flagnetic_flutter` follows the `flagnetic` it requires.

Not promised: anything under `lib/src`, the layout of the Dev Tools screen, and the native side of the store.

## Dev Tools

Testers can override flags on their device in a debug or profile build:

```dart
final client = Flagnetic.client(
  'fn_cli_…',
  baseUrl: Uri.parse('https://flags.example.com'),
  context: flagneticContext,
  options: ClientOptions(store: store, allowOverrides: !kReleaseMode),
);
final devTools = FlagneticDevTools(client: client, store: store);

Navigator.push(context, MaterialPageRoute(
  builder: (_) => FlagneticDevToolsScreen(devTools: devTools),
));
```

- **`FlagneticDevTools`** edits the client's local overrides and persists them in the store under the SDK key's own entry, so a tester's overrides survive a restart. `hasOverrides` is a `ValueListenable<bool>` for a badge. In a release build `set` and `remove` are refused, logging `override_refused` with cause `release_build`, `clear` does nothing, and a persisted override is never restored, whatever `allowOverrides` says: an override cannot ship. An environment marked protected refuses overrides too.
- **`FlagneticDevToolsScreen`** lists every flag the client holds with its value, reason and stage. A boolean flag switches inline; every other type opens a dialog parsed by the flag's type; an overridden flag shows a button removing its override, and the app bar clears them all.

## Checks

```bash
flutter pub get
dart format --output=none --set-exit-if-changed .
flutter analyze --fatal-infos
flutter test
flutter test --platform chrome
```

In this repository `pubspec_overrides.yaml` resolves `flagnetic` from `../dart`, so a change to both packages is tested together; the published package requires the released `flagnetic`.
