import 'transport_stub.dart'
    if (dart.library.io) 'transport_io.dart'
    as platform;

/// An HTTP answer: the status, the headers an SDK acts on (lower-case
/// names) and the decompressed body.
typedef TransportResponse = ({
  int status,
  Map<String, String> headers,
  List<int> body,
});

/// Why a request produced no response.
enum TransportFailure {
  /// No answer within the request timeout.
  timeout,

  /// The connection failed or was dropped.
  network,
}

/// A request that produced no response.
final class TransportException implements Exception {
  /// A failure of [kind] with a [detail].
  const TransportException(this.kind, this.detail);

  /// What failed.
  final TransportFailure kind;

  /// The underlying error.
  final String detail;

  @override
  String toString() => 'TransportException(${kind.name}): $detail';
}

/// An answer whose body arrives over time: the ruleset stream.
typedef TransportStream = ({
  int status,
  Map<String, String> headers,
  Stream<List<int>> body,
});

/// Sends requests and reads answers, decompressing a gzip response.
abstract interface class Transport {
  /// Posts [json] to [url] with [headers], answering within [timeout] or
  /// throwing a [TransportException]. The body is sent as it is; only the
  /// answer is compressed.
  Future<TransportResponse> postJson(
    Uri url,
    Map<String, String> headers,
    List<int> json,
    Duration timeout,
  );

  /// Opens [url] with [headers] and answers with the response head plus a
  /// body that stays open, for `text/event-stream`. The head must arrive
  /// within [timeout]; the body has no deadline. Cancelling the body's
  /// subscription ends the request. Throws a [TransportException] when no
  /// head arrives.
  Future<TransportStream> openStream(
    Uri url,
    Map<String, String> headers,
    Duration timeout,
  );

  /// Aborts what is in flight and releases connections.
  void close();
}

/// The platform's transport: `dart:io` on the VM and in Flutter apps.
Transport defaultTransport() => platform.createTransport();
