import 'transport.dart';

/// No transport on this platform yet: every request fails as a network
/// error, so the client serves last good values or built-in defaults.
Transport createTransport() => _Unsupported();

/// Neither `dart:io` nor a browser.
String hostPlatform() => 'unknown';

/// No page to follow.
void Function()? watchPage({
  required void Function() hidden,
  required void Function() visible,
  required void Function() online,
}) => null;

/// Nothing is sent, so nothing names the platform.
String? defaultPlatformAttribute() => null;

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
  bool beacon(Uri url, String body) => false;

  @override
  void close() {}
}
