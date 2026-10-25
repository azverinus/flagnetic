import 'package:flagnetic/flagnetic.dart';
import 'package:flutter/widgets.dart';

import 'lifecycle.dart';

/// Holds the client for the widgets below it and, unless
/// [observeLifecycle] is off, attaches a [FlagneticLifecycleObserver], so
/// values follow the platform without any plumbing in the app.
///
/// ```dart
/// runApp(FlagneticScope(client: client, child: const MyApp()));
/// ```
///
/// The scope does not own the client: construct it before `runApp` and
/// close it when the app does.
final class FlagneticScope extends StatefulWidget {
  /// A scope serving [client] to [child].
  const FlagneticScope({
    required this.client,
    required this.child,
    this.observeLifecycle = true,
    super.key,
  });

  /// The client the widgets below read.
  final FlagneticClient client;

  /// Whether to drive the client's foreground and background triggers from
  /// the application's lifecycle.
  final bool observeLifecycle;

  /// The widget below this one.
  final Widget child;

  /// The client of the nearest scope above [context].
  ///
  /// Throws [FlutterError] when there is none: a missing scope is the
  /// application's own wiring mistake, visible on the first frame, not a
  /// flag that failed to load.
  static FlagneticClient of(BuildContext context) {
    final scope = context
        .dependOnInheritedWidgetOfExactType<_FlagneticClientScope>();
    if (scope == null) {
      throw FlutterError(
        'No FlagneticScope found above this widget. Wrap the application in '
        'a FlagneticScope, or pass the client to the widget directly.',
      );
    }
    return scope.client;
  }

  /// The client of the nearest scope above [context], or null.
  static FlagneticClient? maybeOf(BuildContext context) => context
      .dependOnInheritedWidgetOfExactType<_FlagneticClientScope>()
      ?.client;

  @override
  State<FlagneticScope> createState() => _FlagneticScopeState();
}

class _FlagneticScopeState extends State<FlagneticScope> {
  FlagneticLifecycleObserver? _observer;

  @override
  void initState() {
    super.initState();
    _observe();
  }

  @override
  void didUpdateWidget(FlagneticScope oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.client == widget.client &&
        oldWidget.observeLifecycle == widget.observeLifecycle) {
      return;
    }
    _observer?.detach();
    _observer = null;
    _observe();
  }

  void _observe() {
    if (!widget.observeLifecycle) return;
    _observer = FlagneticLifecycleObserver(widget.client)..attach();
  }

  @override
  void dispose() {
    _observer?.detach();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) =>
      _FlagneticClientScope(client: widget.client, child: widget.child);
}

final class _FlagneticClientScope extends InheritedWidget {
  const _FlagneticClientScope({required this.client, required super.child});

  final FlagneticClient client;

  @override
  bool updateShouldNotify(_FlagneticClientScope oldWidget) =>
      oldWidget.client != client;
}
