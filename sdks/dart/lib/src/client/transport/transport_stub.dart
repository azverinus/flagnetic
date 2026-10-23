import 'transport.dart';

/// No transport on this platform yet: every request fails as a network
/// error, so the client serves last good values or built-in defaults.
Transport createTransport() => _Unsupported();

final class _Unsupported implements Transport {
  @override
  Future<TransportResponse> postJson(
    Uri url,
    Map<String, String> headers,
    List<int> json,
    Duration timeout,
  ) async => throw const TransportException(
    TransportFailure.network,
    'no HTTP transport on this platform',
  );

  @override
  Future<TransportStream> openStream(
    Uri url,
    Map<String, String> headers,
    Duration timeout,
  ) async => throw const TransportException(
    TransportFailure.network,
    'no HTTP transport on this platform',
  );

  @override
  void close() {}
}
