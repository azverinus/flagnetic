import Cocoa
import FlutterMacOS

public class FlagneticFlutterPlugin: NSObject, FlutterPlugin {
  public static func register(with registrar: FlutterPluginRegistrar) {
    let channel = FlutterMethodChannel(
      name: "flagnetic_flutter/storage", binaryMessenger: registrar.messenger)
    registrar.addMethodCallDelegate(FlagneticFlutterPlugin(), channel: channel)
  }

  public func handle(_ call: FlutterMethodCall, result: @escaping FlutterResult) {
    guard call.method == "backupFreeDirectory" else {
      result(FlutterMethodNotImplemented)
      return
    }
    // The anonymous unit ID belongs to one device: a restored backup, or
    // Quick Start, must not carry it to a new one. Application Support is
    // backed up unless the directory says otherwise. Outside the sandbox
    // Application Support is shared by every app of the user, so the
    // directory goes under the bundle identifier, or every app built with
    // the SDK on this Mac would share one anonymous ID and one event queue.
    do {
      var directory = try FileManager.default.url(
        for: .applicationSupportDirectory,
        in: .userDomainMask,
        appropriateFor: nil,
        create: true
      )
      if let bundle = Bundle.main.bundleIdentifier, !bundle.isEmpty {
        directory.appendPathComponent(bundle, isDirectory: true)
      }
      directory.appendPathComponent("flagnetic-device", isDirectory: true)
      try FileManager.default.createDirectory(
        at: directory,
        withIntermediateDirectories: true
      )
      var values = URLResourceValues()
      values.isExcludedFromBackup = true
      try directory.setResourceValues(values)
      result(directory.path)
    } catch {
      result(FlutterError(code: "unavailable", message: "\(error)", details: nil))
    }
  }
}
