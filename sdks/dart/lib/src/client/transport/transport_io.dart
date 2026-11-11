import 'dart:async';
import 'dart:io';

import 'transport.dart';

/// The `dart:io` transport.
Transport createTransport() => _IoTransport();

/// The operating system the app runs on.
String hostPlatform() => Platform.operatingSystem;

/// No page to follow: the app calls `foreground` and `background`.
void Function()? watchPage({
  required void Function() hidden,
  required void Function() visible,
  required void Function() online,
}) => null;

/// `User-Agent` names the platform.
String? defaultPlatformAttribute() => null;

final class _IoTransport implements Transport {
  final HttpClient _http = HttpClient()..autoUncompress = true;
  final Set<HttpClientRequest> _inFlight = {};

  @override
  Future<TransportResponse> postJson(
    Uri url,
    Map<String, String> headers,
    List<int> json,
    Duration timeout,
  ) =>
      // The body is not compressed: a context is a few hundred bytes, and
      // the evaluate endpoint does not decompress a request.
      _send(() => _http.postUrl(url), headers, json, timeout);

  /// Opens a request with [open], sends [headers] and [body], and reads the
  /// whole answer within [timeout].
  ///
  /// On a timeout the request is abandoned wherever it stands: still
  /// connecting, it is aborted the moment it opens, before anything is sent;
  /// answered, the body subscription is cancelled, which destroys the socket.
  /// `HttpClientRequest.abort` does nothing once the headers are in.
  Future<TransportResponse> _send(
    Future<HttpClientRequest> Function() open,
    Map<String, String> headers,
    List<int>? body,
    Duration timeout,
  ) async {
    HttpClientRequest? request;
    StreamSubscription<List<int>>? reading;
    var abandoned = false;
    try {
      return await () async {
        _http.connectionTimeout = timeout;
        request = await open();
        final req = request!;
        if (abandoned) {
          req.abort();
          throw TimeoutException(null);
        }
        _inFlight.add(req);
        headers.forEach(req.headers.set);
        req.headers.set(HttpHeaders.acceptEncodingHeader, 'gzip');
        if (body != null) req.add(body);
        final response = await req.close();
        final bytes = <int>[];
        final done = Completer<void>();
        reading = response.listen(
          (chunk) {
            bytes.addAll(chunk);
            if (bytes.length > maxResponseBytes && !done.isCompleted) {
              unawaited(reading?.cancel());
              done.completeError(
                TransportException(
                  TransportFailure.network,
                  'answer larger than $maxResponseBytes bytes',
                ),
              );
            }
          },
          onError: (Object e) {
            if (!done.isCompleted) done.completeError(e);
          },
          onDone: () {
            if (!done.isCompleted) done.complete();
          },
          cancelOnError: true,
        );
        await done.future;
        final received = <String, String>{};
        response.headers.forEach((name, values) {
          received[name.toLowerCase()] = values.join(', ');
        });
        return (status: response.statusCode, headers: received, body: bytes);
      }().timeout(timeout);
    } on TimeoutException {
      abandoned = true;
      request?.abort();
      unawaited(reading?.cancel());
      throw TransportException(
        TransportFailure.timeout,
        'no answer within $timeout',
      );
    } on TransportException {
      rethrow;
    } on Object catch (e) {
      throw TransportException(TransportFailure.network, '$e');
    } finally {
      _inFlight.remove(request);
    }
  }

  @override
  Future<TransportStream> openStream(
    Uri url,
    Map<String, String> headers,
    Duration timeout,
  ) async {
    HttpClientRequest? request;
    HttpClientResponse? answered;
    var abandoned = false;
    try {
      final response = await () async {
        _http.connectionTimeout = timeout;
        request = await _http.getUrl(url);
        final req = request!;
        if (abandoned) {
          req.abort();
          throw TimeoutException(null);
        }
        _inFlight.add(req);
        headers.forEach(req.headers.set);
        req.headers
          ..set(HttpHeaders.acceptHeader, 'text/event-stream')
          // The bytes must arrive as the server writes them, so nothing may
          // buffer them on the way.
          ..set(HttpHeaders.acceptEncodingHeader, 'identity')
          ..set(HttpHeaders.cacheControlHeader, 'no-cache');
        final head = await req.close();
        answered = head;
        if (abandoned) {
          unawaited(head.listen(null).cancel());
          throw TimeoutException(null);
        }
        return head;
      }().timeout(timeout);
      final answer = <String, String>{};
      response.headers.forEach((name, values) {
        answer[name.toLowerCase()] = values.join(', ');
      });
      return (
        status: response.statusCode,
        headers: answer,
        body: _untilDone(response, request!),
      );
    } on TimeoutException {
      abandoned = true;
      request?.abort();
      // A head that arrived in the same instant is cancelled too, so no
      // answer is left open that nobody reads.
      final head = answered;
      if (head != null) unawaited(head.listen(null).cancel());
      _inFlight.remove(request);
      throw TransportException(
        TransportFailure.timeout,
        'no answer within $timeout',
      );
    } on Object catch (e) {
      _inFlight.remove(request);
      throw TransportException(TransportFailure.network, '$e');
    }
  }

  /// [response] with [request] removed from the in-flight set once its body
  /// ends, so `close` aborts only what is still open, and every error on the
  /// way turned into a [TransportException]. Cancelling the subscription,
  /// even before the first chunk, ends the connection.
  Stream<List<int>> _untilDone(
    HttpClientResponse response,
    HttpClientRequest request,
  ) {
    late final StreamController<List<int>> out;
    StreamSubscription<List<int>>? body;
    out = StreamController<List<int>>(
      onListen: () {
        body = response.listen(
          out.add,
          onError: (Object e) => out.addError(
            e is TransportException
                ? e
                : TransportException(TransportFailure.network, '$e'),
          ),
          onDone: () {
            _inFlight.remove(request);
            unawaited(out.close());
          },
        );
      },
      onPause: () => body?.pause(),
      onResume: () => body?.resume(),
      onCancel: () {
        _inFlight.remove(request);
        // A body nobody reads may never end by itself: the request is
        // aborted rather than left holding its connection.
        request.abort();
        return body?.cancel();
      },
    );
    return out.stream;
  }

  @override
  bool beacon(Uri url, String body) => false;

  @override
  void close() {
    for (final request in _inFlight) {
      request.abort();
    }
    _http.close(force: true);
  }
}
