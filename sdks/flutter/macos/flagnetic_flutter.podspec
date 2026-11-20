#
# To learn more about a Podspec see http://guides.cocoapods.org/syntax/podspec.html.
# Run `pod lib lint flagnetic_flutter.podspec` to validate before publishing.
#
Pod::Spec.new do |s|
  s.name             = 'flagnetic_flutter'
  s.version          = '0.0.1'
  s.summary          = 'Where the Flagnetic store keeps what must stay on one device.'
  s.description      = <<-DESC
Where the Flagnetic store keeps what must stay on one device.
                       DESC
  s.homepage         = 'https://github.com/azverinus/flagnetic'
  s.license          = { :file => '../LICENSE' }
  s.author           = { 'Flagnetic' => 'https://github.com/azverinus/flagnetic' }

  s.source           = { :path => '.' }
  s.source_files = 'flagnetic_flutter/Sources/flagnetic_flutter/**/*'


  s.dependency 'FlutterMacOS'

  s.platform = :osx, '10.11'
  s.pod_target_xcconfig = { 'DEFINES_MODULE' => 'YES' }
  s.swift_version = '5.0'
end
