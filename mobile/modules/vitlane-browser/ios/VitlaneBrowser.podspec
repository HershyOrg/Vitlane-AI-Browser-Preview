Pod::Spec.new do |s|
  s.name           = 'VitlaneBrowser'
  s.version        = '1.0.0'
  s.summary        = 'Vitlane iOS WebKit browser adapter'
  s.description    = 'A capability-limited WKWebView adapter with a native control boundary.'
  s.author         = 'Vitlane'
  s.homepage       = 'https://github.com/HershyOrg/Vitlane-AI-Browser-Preview'
  s.platforms      = {
    :ios => '16.4'
  }
  s.source         = { git: '' }
  s.static_framework = true

  s.dependency 'ExpoModulesCore'
  s.frameworks = 'WebKit', 'Security', 'CryptoKit'

  # Swift/Objective-C compatibility
  s.pod_target_xcconfig = {
    'DEFINES_MODULE' => 'YES',
  }

  s.source_files = "*.{h,m,mm,swift,hpp,cpp}"
  s.resource_bundles = {
    'VitlaneBrowserResources' => ['Resources/**/*']
  }
end
