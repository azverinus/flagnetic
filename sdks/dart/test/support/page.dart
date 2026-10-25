// The page a browser test runs in, driven through the DOM: the client hears
// the same events a real page sends it.
import 'dart:js_interop';

import 'protocol_runner.dart';

@JS('Object.defineProperty')
external void _defineProperty(JSObject target, String name, JSAny descriptor);

@JS('document')
external _Target get _document;

@JS('window')
external _Target get _window;

extension type _Target._(JSObject _) implements JSObject {
  external bool dispatchEvent(_Event event);
}

@JS('Event')
extension type _Event._(JSObject _) implements JSObject {
  external factory _Event(String type);
}

/// Makes the page [state] (`visible` or `hidden`) and tells it so.
void showPage(String state) {
  _defineProperty(
    _document,
    'visibilityState',
    {'value': state, 'configurable': true}.jsify()!,
  );
  _document.dispatchEvent(_Event('visibilitychange'));
}

/// Tells the page it is being unloaded or put in the back-forward cache.
void firePageHide() => _window.dispatchEvent(_Event('pagehide'));

/// Tells the page the network came back.
void fireOnline() => _window.dispatchEvent(_Event('online'));

/// The test's own page, for the protocol runner.
final class DomPage implements Page {
  /// The page.
  const DomPage();

  @override
  void hide() => showPage('hidden');
  @override
  void show() => showPage('visible');
  @override
  void pageHide() => firePageHide();
  @override
  void online() => fireOnline();
}
