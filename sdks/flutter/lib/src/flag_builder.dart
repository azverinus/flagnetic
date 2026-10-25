import 'package:flagnetic/flagnetic.dart';
import 'package:flutter/widgets.dart';

import 'listenable.dart';
import 'scope.dart';

/// Builds from the value of one flag and rebuilds whenever it changes.
///
/// The type of [defaultValue] names the flag type asked for: `bool` is
/// boolean, `String` is string, `num` is number, anything else is JSON. The
/// first frame already has a value — the last good values from the store,
/// or the built-in default — so a screen never waits on the platform.
///
/// ```dart
/// FlagBuilder<bool>(
///   flagKey: 'checkout',
///   defaultValue: false,
///   builder: (context, showCheckout, detail) =>
///       showCheckout ? const Checkout() : const Basket(),
/// )
/// ```
///
/// [client] defaults to the one of the nearest [FlagneticScope].
final class FlagBuilder<T> extends StatefulWidget {
  /// A builder of [flagKey], falling back to [defaultValue].
  const FlagBuilder({
    required this.flagKey,
    required this.defaultValue,
    required this.builder,
    this.client,
    super.key,
  });

  /// The flag followed.
  final String flagKey;

  /// The built-in default: what this build ships with.
  final T defaultValue;

  /// Builds from the served value and why it was served.
  final Widget Function(BuildContext context, T value, Detail detail) builder;

  /// The client to read; the nearest [FlagneticScope]'s when null.
  final FlagneticClient? client;

  @override
  State<FlagBuilder<T>> createState() => _FlagBuilderState<T>();
}

class _FlagBuilderState<T> extends State<FlagBuilder<T>> {
  FlagListenable<T>? _flag;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _follow();
  }

  @override
  void didUpdateWidget(FlagBuilder<T> oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.flagKey != widget.flagKey ||
        oldWidget.defaultValue != widget.defaultValue ||
        oldWidget.client != widget.client) {
      _follow();
    }
  }

  void _follow() {
    final client = widget.client ?? FlagneticScope.of(context);
    final held = _flag;
    if (held != null &&
        held.client == client &&
        held.flagKey == widget.flagKey &&
        held.defaultValue == widget.defaultValue) {
      return;
    }
    held?.dispose();
    _flag = FlagListenable<T>(client, widget.flagKey, widget.defaultValue);
  }

  @override
  void dispose() {
    _flag?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final flag = _flag!;
    return ListenableBuilder(
      listenable: flag,
      builder: (context, _) => widget.builder(context, flag.value, flag.detail),
    );
  }
}
