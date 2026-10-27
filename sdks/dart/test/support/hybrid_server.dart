// The VM side of a browser test: mock servers that a page of another
// origin calls, driven over the test's channel, plus the protocol fixtures
// a browser cannot read from the checkout, and the scenario report it
// cannot write. remote_server.dart is the
// browser side.
import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:stream_channel/stream_channel.dart';

import 'mock_server.dart';
import 'scenario_report.dart';

/// Sends every protocol fixture, then answers each command with
/// `{id, result}` or `{id, error}`.
Future<void> hybridMain(StreamChannel<Object?> channel, Object? message) async {
  final root = Directory(protocolDir);
  channel.sink.add({
    'fixtures': {
      for (final file in root.listSync(recursive: true).whereType<File>())
        file.path.substring(root.path.length + 1): file.readAsStringSync(),
    },
  });
  final servers = <int, MockServer>{};
  final exchanges = <int, Exchange>{};
  final streams = <int, OpenStream>{};
  var ids = 0;

  Future<Object?> run(Map<String, Object?> command) async {
    switch (command['op']) {
      case 'start':
        final server = await MockServer.start(cors: true);
        servers[++ids] = server;
        return {'server': ids, 'url': '${server.url}'};
      case 'next':
        final server = servers[command['server']]!;
        final exchange = await server.next(
          path: command['path'] as String?,
          timeout: Duration(microseconds: command['timeout_us']! as int),
        );
        exchanges[++ids] = exchange;
        return {
          'exchange': ids,
          'method': exchange.method,
          'path': exchange.path,
          'query': {...exchange.query},
          'headers': exchange.headers,
          'body': base64.encode(exchange.rawBody),
          'arrived_at_us': exchange.arrivedAt.microsecondsSinceEpoch,
        };
      case 'respond':
        final body = command['body'] as String?;
        await exchanges
            .remove(command['exchange'])!
            .respond(
              command['status']! as int,
              headers: (command['headers']! as Map).cast<String, String>(),
              body: body == null ? null : base64.decode(body),
            );
      case 'respond_stream':
        final stream = await exchanges
            .remove(command['exchange'])!
            .respondStream(
              headers: (command['headers']! as Map).cast<String, String>(),
            );
        streams[++ids] = stream;
        return {'stream': ids};
      case 'drop':
        await exchanges.remove(command['exchange'])!.dropConnection();
      case 'send':
        await streams[command['stream']]!.send(command['bytes']! as String);
      case 'closed_by_sdk':
        return streams[command['stream']]!.closedBySdk(
          Duration(microseconds: command['timeout_us']! as int),
        );
      case 'close_stream':
        await streams.remove(command['stream'])!.closeStream();
      case 'answer_all':
        final body = command['body'] as String?;
        await servers[command['server']]!.answerAll(
          command['path']! as String,
          command['status']! as int,
          headers: (command['headers']! as Map).cast<String, String>(),
          body: body == null ? null : base64.decode(body),
        );
      case 'count_received':
        return servers[command['server']]!.countReceived(
          path: command['path'] as String?,
        );
      case 'unanswered_at':
        return servers[command['server']]!.unansweredAt(
          DateTime.fromMicrosecondsSinceEpoch(command['at_us']! as int),
        );
      case 'close':
        await servers.remove(command['server'])!.close();
      case 'report':
        reportPassed((command['passed']! as List<Object?>).cast());
    }
    return null;
  }

  channel.stream.listen((message) async {
    final command = (message! as Map).cast<String, Object?>();
    try {
      channel.sink.add({'id': command['id'], 'result': await run(command)});
    } on TimeoutException catch (e) {
      channel.sink.add({'id': command['id'], 'timeout': '$e'});
    } on Object catch (e) {
      channel.sink.add({'id': command['id'], 'error': '$e'});
    }
  });
}
