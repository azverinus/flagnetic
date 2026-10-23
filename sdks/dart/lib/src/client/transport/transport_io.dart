import 'dart:async';
import 'dart:io';

import 'transport.dart';

/// The `dart:io` transport.
Transport createTransport() => _IoTransport();

/// The operating system the app runs on.
String hostPlatform() => Platform.operatingSystem;

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
  Future<TransportResponse> _send(
    Future<HttpClientRequest> Function() open,
    Map<String, String> headers,
    List<int>? body,
    Duration timeout,
  ) async {
    HttpClientRequest? request;
    try {
      return await () async {
        request = await open();
        final req = request!;
        _inFlight.add(req);
        headers.forEach(req.headers.set);
        req.headers.set(HttpHeaders.acceptEncodingHeader, 'gzip');
        if (body != null) req.add(body);
        final response = await req.close();
        final bytes = <int>[];
        await response.forEach(bytes.addAll);
        final received = <String, String>{};
        response.headers.forEach((name, values) {
          received[name.toLowerCase()] = values.join(', ');
        });
        return (status: response.statusCode, headers: received, body: bytes);
      }().timeout(timeout);
    } on TimeoutException {
      request?.abort();
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
    try {
      final response = await () async {
        request = await _http.getUrl(url);
        final req = request!;
        _inFlight.add(req);
        headers.forEach(req.headers.set);
        req.headers
          ..set(HttpHeaders.acceptHeader, 'text/event-stream')
          // The bytes must arrive as the server writes them, so nothing may
          // buffer them on the way.
          ..set(HttpHeaders.acceptEncodingHeader, 'identity')
          ..set(HttpHeaders.cacheControlHeader, 'no-cache');
        return req.close();
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
      request?.abort();
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
  /// way turned into a [TransportException].
  Stream<List<int>> _untilDone(
    HttpClientResponse response,
    HttpClientRequest request,
  ) async* {
    try {
      yield* response.handleError(
        (Object e) => throw TransportException(TransportFailure.network, '$e'),
        test: (e) => e is! TransportException,
      );
    } finally {
      _inFlight.remove(request);
    }
  }

  @override
  void close() {
    for (final request in _inFlight) {
      request.abort();
    }
    _http.close(force: true);
  }
}
