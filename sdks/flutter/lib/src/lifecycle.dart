import 'package:flagnetic/flagnetic.dart';
import 'package:flutter/widgets.dart';

/// Drives a client's foreground and background triggers from the
/// application's lifecycle. The app resuming reopens the ruleset stream and
/// evaluates again, unless the last attempt is less than
/// [ClientOptions.minimumRefetchGap] old; the app paused, hidden or detached
/// closes the stream, stops the interval and flushes queued events best
/// effort, because an app in the background must hold no connection.
///
/// [FlagneticScope] attaches one by default; attach one yourself only when
/// the app builds its widget tree some other way.
///
/// ```dart
/// final observer = FlagneticLifecycleObserver(client)..attach();
/// // …
/// observer.detach();
/// ```
final class FlagneticLifecycleObserver with WidgetsBindingObserver {
  /// An observer for [client].
  FlagneticLifecycleObserver(this.client);

  /// The client this observer drives.
  final FlagneticClient client;

  /// Starts observing. Safe to call twice.
  ///
  /// A client created while the app is already in the background, by a
  /// background task or a push handler, would otherwise keep its stream and
  /// interval until the next lifecycle change, which may never come; so the
  /// state the app is in now is applied as if it had just changed to it.
  /// Only the background states are: a client starts in the foreground, and
  /// foregrounding it again would evaluate for nothing.
  void attach() {
    final binding = WidgetsBinding.instance..removeObserver(this);
    binding.addObserver(this);
    final state = binding.lifecycleState;
    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.hidden ||
        state == AppLifecycleState.detached) {
      didChangeAppLifecycleState(state!);
    }
  }

  /// Stops observing.
  void detach() => WidgetsBinding.instance.removeObserver(this);

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    switch (state) {
      case AppLifecycleState.resumed:
        client.foreground();
      case AppLifecycleState.paused:
      case AppLifecycleState.hidden:
      case AppLifecycleState.detached:
        client.background();
      case AppLifecycleState.inactive:
      // A transient interruption: a call, the app switcher, a system
      // dialog. The app is still on screen, so nothing changes.
    }
  }
}
