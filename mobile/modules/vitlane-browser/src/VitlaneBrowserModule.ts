import { NativeModule, requireOptionalNativeModule } from 'expo';

/** Marker module. Browser operations are intentionally scoped to a mounted native view. */
declare class VitlaneBrowserModule extends NativeModule<Record<string, never>> {}

export default requireOptionalNativeModule<VitlaneBrowserModule>('VitlaneBrowser');
