import 'dart:convert';

import 'uuid.dart';

/// One event in the shape `POST /sdk/v1/events` takes it: a
/// `type`, an `event_id` the platform deduplicates on and the fields of its
/// kind. It is plain JSON, so the queue persists exactly what it
/// sends and a restart changes nothing about it.
typedef QueuedEvent = Map<String, Object?>;

/// The event types of the ingest envelope.
abstract final class EventTypes {
  /// A unit encountered behaviour that depends on its assignment.
  static const exposure = 'exposure';

  /// Two unit IDs belong to the same person.
  static const alias = 'alias';

  /// Counts per flag, variation and evaluation reason over one interval.
  static const evaluationSummary = 'evaluation_summary';
}

/// The most events one batch may carry.
const maxEventsPerBatch = 500;

/// The most rows one evaluation summary may carry.
const maxSummaryRows = 1000;

/// The largest decompressed envelope the endpoint accepts.
const maxBatchBytes = 1 << 20;

/// Everything an exposure reports about the evaluation that produced it.
/// The SDK's name and version travel in the envelope; the
/// environment and `received_at` are the platform's.
typedef ExposureFields = ({
  String flagKey,
  String experimentKey,
  String variationKey,
  String unitType,
  String unitId,

  /// `experiment`, or `override` when an override's shadow evaluation
  /// landed in the experiment.
  String reason,
  int rulesetVersion,

  /// The experiment block's own `override`: such exposures are counted
  /// separately and never become a first exposure.
  bool override,
  String? platform,
  String? appVersion,
});

/// One exposure event. Its `event_id` is chosen here, when the exposure is
/// recorded, so every retry of the batch carries the same one and the
/// platform drops the repeat.
QueuedEvent exposureEvent(DateTime at, ExposureFields f) => {
  'type': EventTypes.exposure,
  'event_id': uuidV7(at),
  'occurred_at': at.toIso8601String(),
  'experiment_key': f.experimentKey,
  'flag_key': f.flagKey,
  'variation_key': f.variationKey,
  'unit_type': f.unitType,
  'unit_id': f.unitId,
  'platform': ?f.platform,
  'app_version': ?f.appVersion,
  'evaluation_reason': f.reason,
  'ruleset_version': f.rulesetVersion,
  'override': f.override,
};

/// The two unit IDs an alias links, typically an anonymous ID and a user ID
/// after sign-up.
typedef AliasLink = ({
  String fromUnitType,
  String fromUnitId,
  String toUnitType,
  String toUnitId,
});

/// One alias event. It changes no context.
QueuedEvent aliasEvent(DateTime at, AliasLink link) => {
  'type': EventTypes.alias,
  'event_id': uuidV7(at),
  'occurred_at': at.toIso8601String(),
  'from_unit_type': link.fromUnitType,
  'from_unit_id': link.fromUnitId,
  'to_unit_type': link.toUnitType,
  'to_unit_id': link.toUnitId,
};

/// One row of an evaluation summary: one flag, one variation, one
/// evaluation reason and how often it was served. No unit ID, attribute or
/// flag value is in it, by construction.
typedef SummaryRow = ({
  String flag,

  /// Null when no variation was served, as with `fallback` and `error`.
  String? variation,
  String reason,
  int count,
});

/// What was evaluated over one interval, and what an evaluation summary
/// reports besides its counts.
final class SummaryWindow {
  /// A window of [rows] between [start] and [end].
  const SummaryWindow({
    required this.start,
    required this.end,
    required this.rows,
  });

  /// When counting began.
  final DateTime start;

  /// When counting stopped.
  final DateTime end;

  /// The counts, ordered by flag, variation and reason.
  final List<SummaryRow> rows;
}

/// Counts every evaluation per (flag, variation, evaluation reason) for one
/// interval, including `fallback` and `local_override`, so that a flag no
/// build evaluates any more and a forgotten local override are both visible.
final class SummaryCounter {
  /// A counter whose first interval starts now.
  SummaryCounter() : _start = DateTime.now().toUtc();

  final Map<String, SummaryRow> _rows = {};
  DateTime _start;

  /// Whether nothing has been counted since the last [take].
  bool get isEmpty => _rows.isEmpty;

  /// Counts one evaluation of [flag] that served [variation] for [reason].
  void count(String flag, String? variation, String reason) {
    final id = jsonEncode([flag, variation, reason]);
    final held = _rows[id];
    _rows[id] = held == null
        ? (flag: flag, variation: variation, reason: reason, count: 1)
        : (
            flag: held.flag,
            variation: held.variation,
            reason: held.reason,
            count: held.count + 1,
          );
  }

  /// The counts since construction or the last [take], in order.
  List<SummaryRow> get rows => orderRows(_rows.values.toList());

  /// The counts and the interval they cover, resetting the counter.
  SummaryWindow take() {
    final end = DateTime.now().toUtc();
    final window = SummaryWindow(start: _start, end: end, rows: rows);
    _rows.clear();
    _start = end;
    return window;
  }
}

/// Orders summary rows by flag, variation and reason, so that a batch reads
/// the same way every time.
List<SummaryRow> orderRows(List<SummaryRow> rows) {
  int compare(SummaryRow a, SummaryRow b) {
    final byFlag = a.flag.compareTo(b.flag);
    if (byFlag != 0) return byFlag;
    final byVariation = (a.variation ?? '').compareTo(b.variation ?? '');
    return byVariation != 0 ? byVariation : a.reason.compareTo(b.reason);
  }

  return rows.toList()..sort(compare);
}

/// Folds [other] into [rows], adding the counts of rows naming the same
/// flag, variation and reason: how a window whose report never arrived
/// merges into the next one.
List<SummaryRow> mergeRows(List<SummaryRow> rows, List<SummaryRow> other) {
  final merged = <String, SummaryRow>{};
  for (final row in [...rows, ...other]) {
    final id = jsonEncode([row.flag, row.variation, row.reason]);
    final held = merged[id];
    merged[id] = held == null
        ? row
        : (
            flag: row.flag,
            variation: row.variation,
            reason: row.reason,
            count: held.count + row.count,
          );
  }
  return orderRows(merged.values.toList());
}

/// Reads the rows of a queued evaluation summary back, so that a window
/// whose report never arrived can merge into the next one.
List<SummaryRow> rowsOf(QueuedEvent summary) => [
  if (summary['counts'] case final List<Object?> counts)
    for (final row in counts)
      if (row case {'flag': final String flag, 'reason': final String reason})
        (
          flag: flag,
          variation: (row as Map<String, Object?>)['variation'] as String?,
          reason: reason,
          count: switch (row['count']) {
            final int n => n,
            _ => 0,
          },
        ),
];

/// The evaluation summaries one window becomes: its rows in order, at most
/// [maxSummaryRows] to an event, each with its own event ID and all naming
/// the window's interval and the ruleset version held at its end. No rows,
/// no event.
List<QueuedEvent> summaryEvents(
  DateTime at,
  SummaryWindow window,
  int rulesetVersion, {
  String? appVersion,
}) => [
  for (var from = 0; from < window.rows.length; from += maxSummaryRows)
    {
      'type': EventTypes.evaluationSummary,
      'event_id': uuidV7(at),
      'interval_start': window.start.toIso8601String(),
      'interval_end': window.end.toIso8601String(),
      'ruleset_version': rulesetVersion,
      'app_version': ?appVersion,
      'counts': [
        for (final row in window.rows.skip(from).take(maxSummaryRows))
          {
            'flag': row.flag,
            'variation': row.variation,
            'reason': row.reason,
            'count': row.count,
          },
      ],
    },
];

/// One unit's assignment in one experiment: what a repeated exposure
/// repeats.
typedef Assignment = ({
  String key,
  String variation,
  String unitType,
  String unitId,
});

/// The bounded in-process set that suppresses repeats of one assignment.
/// At capacity the oldest assignment is forgotten rather than
/// all of them, so the most recent ones keep being suppressed; the
/// platform's deduplication by event ID and its first exposure stay
/// authoritative, so a forgotten one costs at most a duplicate the platform
/// drops.
final class ExposureSuppressor {
  /// A suppressor holding at most [capacity] assignments.
  ExposureSuppressor(this.capacity);

  /// How many assignments are held before the oldest is forgotten.
  final int capacity;

  // A LinkedHashSet iterates in insertion order, so its first entry is the
  // oldest.
  final Set<String> _seen = {};

  /// True the first time this (experiment, unit type, unit ID, variation)
  /// is seen.
  bool admit(Assignment a) {
    final id = jsonEncode([a.key, a.unitType, a.unitId, a.variation]);
    if (!_seen.add(id)) return false;
    while (_seen.length > capacity && _seen.isNotEmpty) {
      _seen.remove(_seen.first);
    }
    return true;
  }
}

/// How many of [queue]'s leading events one batch may carry: at most
/// [batchSize] and, with [envelopeOverhead] bytes of envelope, under
/// [maxBatchBytes] decompressed. An event too large on its own is still a
/// batch of one, which the platform answers 413 and the caller drops.
int batchLength(List<QueuedEvent> queue, int batchSize, int envelopeOverhead) {
  final budget = maxBatchBytes - envelopeOverhead;
  final limit = batchSize < maxEventsPerBatch ? batchSize : maxEventsPerBatch;
  var size = 0;
  var taken = 0;
  for (final event in queue) {
    if (taken == limit) break;
    // One comma per event after the first.
    final cost = utf8.encode(jsonEncode(event)).length + 1;
    if (taken > 0 && size + cost > budget) break;
    size += cost;
    taken++;
  }
  return taken == 0 ? (queue.isEmpty ? 0 : 1) : taken;
}
