@TestOn('vm')
library;

import 'dart:async';
import 'dart:io';

import 'package:flagnetic/src/client/stream.dart';
import 'package:flagnetic/src/client/transport/transport.dart';
import 'package:test/test.dart';

void main() {
  late Transport transport;

  setUp(() => transport = defaultTransport());
  tearDown(() => transport.close());

  /// A raw server that writes [reply] once a request head has arrived and
  /// completes the returned future when the client closes the connection.
  /// With a null [reply] it never answers.
  Future<(Uri, Future<void>)> rawServer(String? reply) async {
    final raw = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(raw.close);
    final closed = Completer<void>();
    void end() {
      if (!closed.isCompleted) closed.complete();
    }

    raw.listen((socket) {
      var answered = false;
      socket.listen(
        (_) {
          if (answered || reply == null) return;
          answered = true;
          socket.write(reply);
        },
        onDone: end,
        onError: (Object _) => end(),
        cancelOnError: true,
      );
    });
    return (Uri.parse('http://127.0.0.1:${raw.port}/x'), closed.future);
  }

  Future<TransportException> failure(Future<Object?> call) async {
    try {
      await call;
    } on TransportException catch (e) {
      return e;
    }
    fail('expected a TransportException');
  }

  test(
    'a header the HTTP library refuses is reported without its value',
    () async {
      final (url, _) = await rawServer(null);
      final headers = {'Authorization': 'Bearer fn_cli_SECRETKEY0\n'};
      final post = await failure(
        transport.postJson(url, headers, [123], const Duration(seconds: 2)),
      );
      final stream = await failure(
        transport.openStream(url, headers, const Duration(seconds: 2)),
      );
      for (final e in [post, stream]) {
        expect(e.kind, TransportFailure.network);
        expect('$e', isNot(contains('SECRET')));
        expect(e.detail, contains('FormatException'));
      }
    },
  );

  test('a post whose body outlasts the timeout is cut off', () async {
    final (url, closed) = await rawServer(
      'HTTP/1.1 200 OK\r\nContent-Length: 1000\r\n\r\nx',
    );
    final e = await failure(
      transport.postJson(url, {}, [123], const Duration(milliseconds: 300)),
    );
    expect(e.kind, TransportFailure.timeout);
    await closed.timeout(const Duration(seconds: 5));
  });

  test('a post never answered times out and its connection closes', () async {
    final (url, closed) = await rawServer(null);
    final e = await failure(
      transport.postJson(url, {}, [123], const Duration(milliseconds: 200)),
    );
    expect(e.kind, TransportFailure.timeout);
    await closed.timeout(const Duration(seconds: 5));
  });

  test('an endless body is refused past the cap', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((request) async {
      final chunk = List<int>.filled(1 << 20, 0x20);
      var gone = false;
      final ended = request.response.done.then<void>(
        (_) {},
        onError: (Object _) {},
      );
      ended.whenComplete(() => gone = true).ignore();
      try {
        while (!gone) {
          request.response.add(chunk);
          await request.response.flush();
          // Yield, so a closed connection is noticed.
          await Future<void>.delayed(Duration.zero);
        }
      } on Object {
        // The client went away.
      }
    });
    final url = Uri.parse('http://127.0.0.1:${server.port}/x');
    const timeout = Duration(seconds: 30);
    final e = await failure(transport.postJson(url, {}, [123], timeout));
    expect(e.kind, TransportFailure.network);
    expect(e.detail, contains('larger than'));
  });

  test('a stream whose head never comes closes when it times out', () async {
    final (url, closed) = await rawServer(null);
    final e = await failure(
      transport.openStream(url, {}, const Duration(milliseconds: 200)),
    );
    expect(e.kind, TransportFailure.timeout);
    await closed.timeout(const Duration(seconds: 5));
  });

  test('a stream cancelled by the reader closes its connection', () async {
    final (url, closed) = await rawServer(
      'HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n'
      'Transfer-Encoding: chunked\r\n\r\n5\r\n: hb\n\r\n',
    );
    final stream = await transport.openStream(
      url,
      {},
      const Duration(seconds: 5),
    );
    final sub = stream.body.listen(null);
    await Future<void>.delayed(const Duration(milliseconds: 100));
    await sub.cancel();
    await closed.timeout(const Duration(seconds: 5));
  });

  group('SseParser', () {
    test('a line without an end past the bound overflows', () {
      final parser = SseParser();
      expect(
        () => parser.add(List.filled(maxSseLineBytes + 1, 0x61), (_) {}),
        throwsA(isA<SseOverflow>()),
      );
    });

    test('data gathered past the bound overflows', () {
      final parser = SseParser();
      final line = 'data: ${'a' * 40000}\n'.codeUnits;
      parser.add(line, (_) {});
      expect(() => parser.add(line, (_) {}), throwsA(isA<SseOverflow>()));
    });

    test('lines within the bound still dispatch', () {
      final parser = SseParser();
      final events = <SseEvent>[];
      parser.add(
        'event: ruleset\ndata: {"version":3}\n\n'.codeUnits,
        events.add,
      );
      expect(events.single.data, '{"version":3}');
    });
  });
}
