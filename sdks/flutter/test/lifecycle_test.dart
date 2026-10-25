// The lifecycle observer drives the client's foreground and background
// triggers, and FlagneticScope attaches one.
import 'package:flagnetic_flutter/flagnetic_flutter.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';

import 'support/offline_client.dart';

void main() {
  testWidgets('resumed foregrounds, paused, hidden and detached background, '
      'inactive changes nothing', (tester) async {
    final client = offlineClient({'checkout': true});
    addTearDown(client.close);
    final observer = FlagneticLifecycleObserver(client)..attach();
    addTearDown(observer.detach);
    expect(client.isForeground, isTrue);

    for (final state in [
      AppLifecycleState.paused,
      AppLifecycleState.hidden,
      AppLifecycleState.detached,
    ]) {
      observer.didChangeAppLifecycleState(AppLifecycleState.resumed);
      expect(client.isForeground, isTrue, reason: 'resumed');
      observer.didChangeAppLifecycleState(state);
      expect(client.isForeground, isFalse, reason: '$state');
    }

    observer.didChangeAppLifecycleState(AppLifecycleState.inactive);
    expect(client.isForeground, isFalse, reason: 'inactive changes nothing');
    observer.didChangeAppLifecycleState(AppLifecycleState.resumed);
    observer.didChangeAppLifecycleState(AppLifecycleState.inactive);
    expect(client.isForeground, isTrue, reason: 'inactive changes nothing');
  });

  testWidgets('a scope observes the lifecycle, and stops on dispose', (
    tester,
  ) async {
    final client = offlineClient({'checkout': true});
    addTearDown(client.close);
    await tester.pumpWidget(
      FlagneticScope(client: client, child: const SizedBox()),
    );
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    expect(client.isForeground, isFalse);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    expect(client.isForeground, isTrue);

    await tester.pumpWidget(const SizedBox());
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    expect(
      client.isForeground,
      isTrue,
      reason: 'the observer detached with the scope',
    );
  });

  testWidgets('a scope can leave the lifecycle alone', (tester) async {
    final client = offlineClient({'checkout': true});
    addTearDown(client.close);
    await tester.pumpWidget(
      FlagneticScope(
        client: client,
        observeLifecycle: false,
        child: const SizedBox(),
      ),
    );
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    expect(client.isForeground, isTrue);
  });

  testWidgets('attaching twice observes once', (tester) async {
    final client = offlineClient({'checkout': true});
    addTearDown(client.close);
    final observer = FlagneticLifecycleObserver(client)
      ..attach()
      ..attach();
    addTearDown(observer.detach);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    expect(client.isForeground, isFalse);
    observer.detach();
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    expect(client.isForeground, isFalse, reason: 'detached for good');
  });
}
