import 'dart:async';
import 'dart:js_interop';
import 'dart:typed_data';

import 'transport.dart';

// The browser's own APIs, bound by hand: the package has no dependency, and
// it needs only these few members.

@JS('fetch')
external JSPromise<_Response> _fetch(String url, _RequestInit init);

@JS('navigator')
external _Navigator? get _navigator;

@JS('document')
external _EventTarget? get _document;

@JS('window')
external _EventTarget? get _window;

extension type _RequestInit._(JSObject _) implements JSObject {
  external factory _RequestInit({
    String method,
    JSAny headers,
    JSUint8Array? body,
    String mode,
    String credentials,
    String cache,
    _AbortSignal signal,
  });
}

extension type _Response._(JSObject _) implements JSObject {
  external int get status;
  external _Headers get headers;
  external _ReadableStream? get body;
  external JSPromise<JSArrayBuffer> arrayBuffer();
}

extension type _Headers._(JSObject _) implements JSObject {
  external void forEach(JSFunction callback);
}

extension type _ReadableStream._(JSObject _) implements JSObject {
  external _Reader getReader();
}

extension type _Reader._(JSObject _) implements JSObject {
  external JSPromise<_ReadResult> read();
  external JSPromise<JSAny?> cancel();
}

extension type _ReadResult._(JSObject _) implements JSObject {
  external bool get done;
  external JSUint8Array? get value;
}

@JS('AbortController')
extension type _AbortController._(JSObject _) implements JSObject {
  external factory _AbortController();
  external _AbortSignal get signal;
  external void abort();
}

extension type _AbortSignal._(JSObject _) implements JSObject {}

extension type _Navigator._(JSObject _) implements JSObject {
  external bool sendBeacon(String url, String data);
}

extension type _EventTarget._(JSObject _) implements JSObject {
  external void addEventListener(String type, JSFunction listener);
  external void removeEventListener(String type, JSFunction listener);
  external String? get visibilityState;
}

/// The browser transport: `fetch`, and `navigator.sendBeacon` for the last
/// batch of a page that is going away.
Transport createTransport() => _WebTransport();

/// A browser names no operating system.
String hostPlatform() => 'web';

/// The page's visibility, its unloading and the network's return, from
/// `document` and `window`.
void Function()? watchPage({
  required void Function() hidden,
  required void Function() visible,
  required void Function() online,
}) {
  final document = _document;
  final window = _window;
  // A worker has neither: nothing hides it, and the app drives the client.
  if (document == null || window == null) return null;
  bool shown() => document.visibilityState != 'hidden';
  final onVisibility = ((JSAny? _) => shown() ? visible() : hidden()).toJS;
  final onPageHide = ((JSAny? _) => hidden()).toJS;
  final onOnline = ((JSAny? _) {
    // A hidden tab runs nothing; its next `visible` refetches.
    if (shown()) online();
  }).toJS;
  document.addEventListener('visibilitychange', onVisibility);
  window
    ..addEventListener('pagehide', onPageHide)
    ..addEventListener('online', onOnline);
  return () {
    document.removeEventListener('visibilitychange', onVisibility);
    window
      ..removeEventListener('pagehide', onPageHide)
      ..removeEventListener('online', onOnline);
  };
}

/// A browser cannot send `User-Agent`, so the platform travels in the
/// context and the events envelope.
String? defaultPlatformAttribute() => 'web';

final class _WebTransport implements Transport {
  final Set<_AbortController> _inFlight = {};

  @override
  Future<TransportResponse> postJson(
    Uri url,
    Map<String, String> headers,
    List<int> json,
    Duration timeout,
  ) async {
    final abort = _AbortController();
    _inFlight.add(abort);
    try {
      return await () async {
        final response = await _fetch(
          url.toString(),
          _RequestInit(
            method: 'POST',
            headers: _allowed(headers).jsify()!,
            body: Uint8List.fromList(json).toJS,
            mode: 'cors',
            credentials: 'omit',
            cache: 'no-store',
            signal: abort.signal,
          ),
        ).toDart;
        final body = await response.arrayBuffer().toDart;
        return (
          status: response.status,
          headers: _headersOf(response),
          body: body.toDart.asUint8List(),
        );
      }().timeout(timeout);
    } on TimeoutException {
      abort.abort();
      throw TransportException(
        TransportFailure.timeout,
        'no answer within $timeout',
      );
    } on Object catch (e) {
      // `fetch` rejects with a TypeError for a network failure, a refused
      // CORS check and an abort alike.
      throw TransportException(TransportFailure.network, '$e');
    } finally {
      _inFlight.remove(abort);
    }
  }

  /// The stream through `fetch` with a streamed body rather than
  /// `EventSource`: an `EventSource` cannot send `Last-Event-ID` on a
  /// reconnect the SDK makes, hides the `: hb` heartbeats that tell a quiet
  /// stream from a dead one, and hides a 401 or 403. The key moves from
  /// `Authorization` to the query as `sdk_key`, which the stream accepts
  /// from a client key.
  @override
  Future<TransportStream> openStream(
    Uri url,
    Map<String, String> headers,
    Duration timeout,
  ) async {
    final key = _bearer(headers);
    final target = key == null
        ? url
        : url.replace(
            queryParameters: {...url.queryParameters, 'sdk_key': key},
          );
    final sent = _allowed(headers)
      ..removeWhere((name, _) => name.toLowerCase() == 'authorization')
      ..['Accept'] = 'text/event-stream';
    final abort = _AbortController();
    _inFlight.add(abort);
    final _Response response;
    try {
      response = await _fetch(
        target.toString(),
        _RequestInit(
          method: 'GET',
          headers: sent.jsify()!,
          mode: 'cors',
          credentials: 'omit',
          cache: 'no-store',
          signal: abort.signal,
        ),
      ).toDart.timeout(timeout);
    } on TimeoutException {
      abort.abort();
      _inFlight.remove(abort);
      throw TransportException(
        TransportFailure.timeout,
        'no answer within $timeout',
      );
    } on Object catch (e) {
      _inFlight.remove(abort);
      throw TransportException(TransportFailure.network, '$e');
    }
    return (
      status: response.status,
      headers: _headersOf(response),
      body: _read(response.body, abort),
    );
  }

  /// The chunks of [body] as they arrive. Cancelling the subscription aborts
  /// the request, which ends the connection.
  Stream<List<int>> _read(_ReadableStream? body, _AbortController abort) {
    late final StreamController<List<int>> out;
    final reader = body?.getReader();
    var cancelled = false;
    Future<void> pump() async {
      try {
        while (reader != null && !cancelled) {
          final chunk = await reader.read().toDart;
          if (chunk.done || cancelled) break;
          final value = chunk.value;
          if (value != null) out.add(value.toDart);
        }
      } on Object catch (e) {
        if (!cancelled) {
          out.addError(TransportException(TransportFailure.network, '$e'));
        }
      } finally {
        _inFlight.remove(abort);
        if (!cancelled) await out.close();
      }
    }

    out = StreamController<List<int>>(
      onListen: () => unawaited(pump()),
      onCancel: () {
        cancelled = true;
        _inFlight.remove(abort);
        abort.abort();
        // The read in progress rejects once aborted; nothing waits for it.
        unawaited(
          reader?.cancel().toDart.then<void>((_) {}, onError: (Object _) {}),
        );
      },
    );
    return out.stream;
  }

  @override
  bool beacon(Uri url, String body) {
    final navigator = _navigator;
    if (navigator == null) return false;
    try {
      // A string is sent as `text/plain;charset=UTF-8`: no preflight, which
      // a page that is going away would not wait for.
      return navigator.sendBeacon(url.toString(), body);
    } on Object {
      return false;
    }
  }

  @override
  void close() {
    for (final abort in [..._inFlight]) {
      abort.abort();
    }
    _inFlight.clear();
  }

  /// [headers] without `User-Agent`, which a browser sets itself and which
  /// the API's CORS answer does not allow a script to send.
  static Map<String, String> _allowed(Map<String, String> headers) => {
    for (final MapEntry(:key, :value) in headers.entries)
      if (key.toLowerCase() != 'user-agent') key: value,
  };

  /// The bearer token of [headers], when they carry one.
  static String? _bearer(Map<String, String> headers) {
    for (final MapEntry(:key, :value) in headers.entries) {
      if (key.toLowerCase() == 'authorization' && value.startsWith('Bearer ')) {
        return value.substring('Bearer '.length);
      }
    }
    return null;
  }

  /// The response headers a script may read, lower-case names.
  static Map<String, String> _headersOf(_Response response) {
    final headers = <String, String>{};
    response.headers.forEach(
      ((String value, String name) {
        headers[name.toLowerCase()] = value;
      }).toJS,
    );
    return headers;
  }
}
