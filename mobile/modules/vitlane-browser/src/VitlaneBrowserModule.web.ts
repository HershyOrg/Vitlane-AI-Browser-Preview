import { registerWebModule, NativeModule } from 'expo';

// VitlaneBrowserModule is not available on the web platform.
class VitlaneBrowserModule extends NativeModule<{}> {}

export default registerWebModule(VitlaneBrowserModule, 'VitlaneBrowser');
