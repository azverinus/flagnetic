# Example

A client constructed before `runApp`, held by a `FlagneticScope`, and a widget rebuilding when its flag changes. The package [README](https://github.com/azverinus/flagnetic/tree/main/sdks/flutter#readme) covers the listenables, the store and the Dev Tools screen.

```dart
import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter/material.dart';

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
    options: ClientOptions(store: store),
  );
  runApp(FlagneticScope(client: client, child: const App()));
}

class App extends StatelessWidget {
  const App({super.key});

  @override
  Widget build(BuildContext context) => MaterialApp(
    home: FlagBuilder<bool>(
      flagKey: 'checkout',
      defaultValue: false,
      builder: (context, showCheckout, detail) =>
          Text(showCheckout ? 'New checkout' : 'Basket'),
    ),
  );
}
```
