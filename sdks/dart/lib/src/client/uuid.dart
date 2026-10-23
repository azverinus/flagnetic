import 'dart:math';

final Random _random = () {
  try {
    return Random.secure();
  } on UnsupportedError {
    return Random();
  }
}();

/// A lowercase UUIDv7 for [time]: 48 bits of Unix milliseconds, then random
/// bits, so event IDs sort by creation and never collide across devices.
String uuidV7(DateTime time) {
  final ms = time.millisecondsSinceEpoch;
  final bytes = List<int>.generate(16, (_) => _random.nextInt(256));
  for (var i = 0; i < 6; i++) {
    bytes[i] = (ms ~/ pow(2, 8 * (5 - i))) & 0xff;
  }
  bytes[6] = 0x70 | (bytes[6] & 0x0f);
  bytes[8] = 0x80 | (bytes[8] & 0x3f);
  final hex = [for (final b in bytes) b.toRadixString(16).padLeft(2, '0')];
  return '${hex.sublist(0, 4).join()}-${hex.sublist(4, 6).join()}-'
      '${hex.sublist(6, 8).join()}-${hex.sublist(8, 10).join()}-'
      '${hex.sublist(10).join()}';
}
