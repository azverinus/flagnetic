import 'transport_stub.dart'
    if (dart.library.io) 'transport_io.dart'
    if (dart.library.js_interop) 'transport_web.dart'
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

  /// Hands [body], an events envelope, to the browser to post to [url] as
  /// `text/plain` after the page has gone, with no answer to read. True
  /// when the browser took it; false when it refused it (over its quota)
  /// or where there is no such browser facility.
  bool beacon(Uri url, String body);

  /// Aborts what is in flight and releases connections.
  void close();
}

/// The platform's transport: `dart:io` on the VM and in Flutter apps,
/// `fetch` in a browser.
Transport defaultTransport() => platform.createTransport();

/// Calls [hidden] when the page is hidden or unloaded, [visible] when it is
/// shown again and [online] when the network comes back while it is shown,
/// until the returned function is called. Null where there is no page:
/// outside a browser the app calls `foreground` and `background` itself.
void Function()? watchPage({
  required void Function() hidden,
  required void Function() visible,
  required void Function() online,
}) => platform.watchPage(hidden: hidden, visible: visible, online: online);

/// The `platform` attribute and envelope member an SDK sends when the app
/// names none: `web` in a browser, which cannot send `User-Agent`, so the
/// platform travels in what is posted; null elsewhere, where `User-Agent`
/// names it.
String? defaultPlatformAttribute() => platform.defaultPlatformAttribute();

/// The platform `User-Agent` names when the options name none: the operating
/// system (`android`, `ios`, `macos`, …) wherever `dart:io` runs, so the SDK
/// inventory tells devices apart without the app configuring anything.
String hostPlatform() => platform.hostPlatform();
