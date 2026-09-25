import { requireNativeView } from 'expo';
import * as React from 'react';
import { Platform } from 'react-native';

import {
  BrowserAuthorizedCommand,
  VitlaneBrowserRef,
  VitlaneBrowserViewProps,
  validateBrowserCommand,
} from './VitlaneBrowser.types';

type NativeBrowserRef = VitlaneBrowserRef;
type NativeBrowserProps = VitlaneBrowserViewProps & React.RefAttributes<NativeBrowserRef>;

let NativeView: React.ComponentType<NativeBrowserProps> | undefined;

function nativeView(): React.ComponentType<NativeBrowserProps> {
  if (Platform.OS !== 'ios') {
    throw new Error('VitlaneBrowserView uses WKWebView and is available only on iOS.');
  }
  NativeView ??= requireNativeView<NativeBrowserProps>('VitlaneBrowser');
  return NativeView;
}

const VitlaneBrowserView = React.forwardRef<VitlaneBrowserRef, VitlaneBrowserViewProps>(
  function VitlaneBrowserView(props, forwardedRef) {
    const nativeRef = React.useRef<NativeBrowserRef>(null);
    const View = nativeView();

    React.useImperativeHandle(
      forwardedRef,
      () => ({
        beginRun: binding => nativeRef.current!.beginRun(binding),
        observe: () => nativeRef.current!.observe(),
        execute: (command: BrowserAuthorizedCommand) => {
          const validation = validateBrowserCommand(command);
          if (!validation.ok) {
            return Promise.reject(new Error(`${validation.code}: ${validation.message}`));
          }
          return nativeRef.current!.execute(command);
        },
        stopAgent: reason => nativeRef.current!.stopAgent(reason),
        takeOver: () => nativeRef.current!.takeOver(),
        resumeAgent: () => nativeRef.current!.resumeAgent(),
        goBack: () => nativeRef.current!.goBack(),
        reload: () => nativeRef.current!.reload(),
      }),
      [],
    );

    return <View ref={nativeRef} {...props} />;
  },
);

export default VitlaneBrowserView;
