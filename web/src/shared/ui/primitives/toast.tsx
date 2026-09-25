"use client"

import * as React from "react"
import { Toast as ToastPrimitive } from "radix-ui"
import { cn } from "@/shared/lib/utils"

// shadcn's Radix Toast composition, styled by the consuming product's tokens.
const ToastProvider = ToastPrimitive.Provider
const ToastTitle = ToastPrimitive.Title
const ToastDescription = ToastPrimitive.Description
const ToastClose = ToastPrimitive.Close
function Toast({className,...props}:React.ComponentProps<typeof ToastPrimitive.Root>) {
 return <ToastPrimitive.Root data-slot="toast" className={cn(className)} {...props} />
}
function ToastViewport({className,...props}:React.ComponentProps<typeof ToastPrimitive.Viewport>) {
 return <ToastPrimitive.Viewport data-slot="toast-viewport" className={cn(className)} {...props} />
}
export {ToastProvider,Toast,ToastTitle,ToastDescription,ToastClose,ToastViewport}
