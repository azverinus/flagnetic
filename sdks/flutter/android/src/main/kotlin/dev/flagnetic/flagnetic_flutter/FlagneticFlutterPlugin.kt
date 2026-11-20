package dev.flagnetic.flagnetic_flutter

import android.content.Context
import io.flutter.embedding.engine.plugins.FlutterPlugin
import io.flutter.plugin.common.MethodCall
import io.flutter.plugin.common.MethodChannel
import io.flutter.plugin.common.MethodChannel.MethodCallHandler
import io.flutter.plugin.common.MethodChannel.Result
import java.io.File

/**
 * Answers where the default store keeps what must stay on this device.
 *
 * The anonymous unit ID belongs to one device: Auto Backup and device-to-device
 * transfer carry the app's `files` directory and its shared preferences to a
 * new phone, but never `noBackupFilesDir`.
 */
class FlagneticFlutterPlugin :
    FlutterPlugin,
    MethodCallHandler {
    private lateinit var channel: MethodChannel
    private lateinit var context: Context

    override fun onAttachedToEngine(flutterPluginBinding: FlutterPlugin.FlutterPluginBinding) {
        context = flutterPluginBinding.applicationContext
        channel = MethodChannel(flutterPluginBinding.binaryMessenger, "flagnetic_flutter/storage")
        channel.setMethodCallHandler(this)
    }

    override fun onMethodCall(
        call: MethodCall,
        result: Result
    ) {
        if (call.method != "backupFreeDirectory") {
            result.notImplemented()
            return
        }
        val directory = File(context.noBackupFilesDir, "flagnetic")
        if (!directory.isDirectory && !directory.mkdirs()) {
            result.error("unavailable", "cannot create ${directory.path}", null)
            return
        }
        result.success(directory.path)
    }

    override fun onDetachedFromEngine(binding: FlutterPlugin.FlutterPluginBinding) {
        channel.setMethodCallHandler(null)
    }
}
