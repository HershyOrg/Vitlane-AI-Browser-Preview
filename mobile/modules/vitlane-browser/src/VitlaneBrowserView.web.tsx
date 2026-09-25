import { VitlaneBrowserViewProps } from './VitlaneBrowser.types';

// VitlaneBrowserView is not available on the web platform.
export default function VitlaneBrowserView(_props: VitlaneBrowserViewProps) {
  throw new Error('VitlaneBrowserView is not available on the web platform.');
}
