# flagnetic_flutter

Flutter widgets for Flagnetic feature flags and experiments, served by a self-hosted [Flagnetic](https://github.com/azverinus/flagnetic) instance: a scope holding the client, a builder widget rebuilding when a flag changes, listenables for a flag and for the fallback chain's stage, a lifecycle observer, a store on every platform and a Dev Tools screen for local overrides. The client itself lives in [`flagnetic`](https://pub.dev/packages/flagnetic), which this package re-exports, so an app imports one package.

Requires Flutter 3.35 or newer, on iOS, Android, desktop and the web.

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
- **`FlagBuilder<T>`** builds from a flag's value and rebuilds when it changes, reading the client of the nearest scope unless one is passed. The type of `defaultValue` names the flag type asked for: `bool` is boolean, `String` is string, `num` is number, anything else is JSON. The first frame already has a value, the store's last good values or the built-in default, so a screen never waits on the network.
- **`FlagListenable<T>`** and **`StageListenable`** are `ValueListenable`s over the client's change and stage listeners. `FlagListenable.detail` says why the current value was served. Call `dispose` when they are no longer used; `FlagBuilder` does it for you.
- **`FlagneticLifecycleObserver`** calls `client.foreground()` when the app resumes and `client.background()` when it is paused, hidden or detached; `inactive` is a transient interruption (a call, the app switcher) and changes nothing. In the foreground the client holds the ruleset stream, so a kill switch reaches an open app within seconds; in the background it holds no connection, and resuming refetches unless the last attempt is less than 10 seconds old. On the web the client already follows the page's visibility by itself, and the observer's calls change nothing more.
- **`FlutterStore`** is the default `FlagneticStore`. On a device, each unit set's last good values, a whole evaluate response, go to their own file under the application support directory, written beside it and renamed so a crash never leaves half a document; the index of unit sets, the event queue and the anonymous unit IDs go to shared preferences. On the web every entry lives in shared preferences. A cold start offline serves the last good values, and events recorded offline are sent on a later start. A store that throws never breaks a flag check: the client logs `store_failed` and serves its built-in defaults.

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
- **`FlagneticDevToolsScreen`** lists every flag the client holds with its value, reason and stage. A boolean flag toggles inline; every other type opens a dialog parsed by the flag's type; an overridden flag shows a button removing its override, and the app bar clears them all.

## Checks

```bash
flutter pub get
dart format --output=none --set-exit-if-changed .
flutter analyze --fatal-infos
flutter test
flutter test --platform chrome
```

In this repository `pubspec_overrides.yaml` resolves `flagnetic` from `../dart`, so a change to both packages is tested together; the published package requires the released `flagnetic`.
