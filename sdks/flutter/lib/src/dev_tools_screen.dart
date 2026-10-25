import 'dart:convert';

import 'package:flagnetic/flagnetic.dart';
import 'package:flutter/material.dart';

import 'dev_tools.dart';

/// Lists every flag the client currently holds — key, value, reason and
/// stage — and lets QA edit or clear local overrides.
///
/// A boolean flag toggles inline; every other type opens a dialog with a
/// text field, parsed by the flag's type. Refused edits (a release build, a
/// protected environment) simply do nothing, exactly as [FlagneticDevTools]
/// does: nothing here needs to know why.
///
/// ```dart
/// Navigator.push(context, MaterialPageRoute(
///   builder: (_) => FlagneticDevToolsScreen(devTools: devTools),
/// ));
/// ```
final class FlagneticDevToolsScreen extends StatefulWidget {
  /// A screen editing the overrides of [devTools].
  const FlagneticDevToolsScreen({required this.devTools, super.key});

  /// The Dev Tools helper this screen edits.
  final FlagneticDevTools devTools;

  @override
  State<FlagneticDevToolsScreen> createState() =>
      _FlagneticDevToolsScreenState();
}

class _FlagneticDevToolsScreenState extends State<FlagneticDevToolsScreen> {
  late final void Function() _removeChange;

  @override
  void initState() {
    super.initState();
    _removeChange = widget.devTools.client.onChange((_) => setState(() {}));
  }

  @override
  void dispose() {
    _removeChange();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final devTools = widget.devTools;
    final client = devTools.client;
    final flags = client.allFlags;
    final hasOverrides = flags.any((f) => f.overridden);
    return Scaffold(
      appBar: AppBar(
        title: const Text('Flagnetic Dev Tools'),
        actions: [
          IconButton(
            icon: const Icon(Icons.clear_all),
            tooltip: 'Clear all overrides',
            onPressed: hasOverrides ? devTools.clear : null,
          ),
        ],
      ),
      body: Column(
        children: [
          _StatusBar(stage: client.stage, releaseMode: devTools.isReleaseBuild),
          Expanded(
            child: flags.isEmpty
                ? const Center(child: Text('No flags yet'))
                : ListView.builder(
                    itemCount: flags.length,
                    itemBuilder: (context, index) =>
                        _FlagRow(devTools: devTools, flag: flags[index]),
                  ),
          ),
        ],
      ),
    );
  }
}

class _StatusBar extends StatelessWidget {
  const _StatusBar({required this.stage, required this.releaseMode});

  final Stage stage;
  final bool releaseMode;

  @override
  Widget build(BuildContext context) => Container(
    width: double.infinity,
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
    color: Theme.of(context).colorScheme.surfaceContainerHighest,
    child: Text(
      releaseMode
          ? 'stage: ${stage.wireName} · overrides are refused in a release build'
          : 'stage: ${stage.wireName}',
    ),
  );
}

class _FlagRow extends StatelessWidget {
  const _FlagRow({required this.devTools, required this.flag});

  final FlagneticDevTools devTools;
  final FlagSummary flag;

  @override
  Widget build(BuildContext context) {
    final isBoolean = flag.type == 'boolean';
    return ListTile(
      title: Text(flag.key),
      subtitle: Text(
        '${flag.value} · ${flag.reason} · ${devTools.client.stage.wireName}',
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (flag.overridden)
            IconButton(
              icon: const Icon(Icons.undo),
              tooltip: 'Remove override',
              onPressed: () => devTools.remove(flag.key),
            ),
          if (isBoolean)
            Switch(
              value: flag.value == true,
              onChanged: (value) => devTools.set(flag.key, value),
            )
          else
            IconButton(
              icon: const Icon(Icons.edit),
              tooltip: 'Override',
              onPressed: () => _edit(context),
            ),
        ],
      ),
      onTap: isBoolean ? null : () => _edit(context),
    );
  }

  Future<void> _edit(BuildContext context) async {
    final value = await showDialog<_EditResult>(
      context: context,
      builder: (context) => _EditDialog(flag: flag),
    );
    if (value == null) return;
    devTools.set(flag.key, value.value);
  }
}

/// What an edit dialog returns: the parsed value, wrapped so `null` (a JSON
/// `null`) is distinguishable from "cancelled".
final class _EditResult {
  const _EditResult(this.value);
  final Object? value;
}

class _EditDialog extends StatefulWidget {
  const _EditDialog({required this.flag});

  final FlagSummary flag;

  @override
  State<_EditDialog> createState() => _EditDialogState();
}

class _EditDialogState extends State<_EditDialog> {
  late final TextEditingController _controller = TextEditingController(
    text: switch (widget.flag.type) {
      'json' => jsonEncode(widget.flag.value),
      _ => '${widget.flag.value}',
    },
  );
  String? _error;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text('Override ${widget.flag.key}'),
    content: TextField(
      controller: _controller,
      autofocus: true,
      keyboardType: widget.flag.type == 'number'
          ? const TextInputType.numberWithOptions(decimal: true, signed: true)
          : TextInputType.text,
      decoration: InputDecoration(errorText: _error),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('Cancel'),
      ),
      TextButton(onPressed: _save, child: const Text('Save')),
    ],
  );

  void _save() {
    final text = _controller.text;
    final Object? value;
    try {
      value = switch (widget.flag.type) {
        'string' => text,
        'number' => num.parse(text),
        'json' => jsonDecode(text),
        _ => text,
      };
    } on FormatException {
      setState(() => _error = 'Not a valid ${widget.flag.type} value');
      return;
    }
    Navigator.pop(context, _EditResult(value));
  }
}
