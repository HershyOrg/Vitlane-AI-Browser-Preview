import { cn } from "@/shared/lib/utils"
import { Loader2Icon } from "lucide-react"
import { useLocale } from "@/shared/i18n"

function Spinner({ className, ...props }: React.ComponentProps<"svg">) {
  const { l } = useLocale()
  return (
    <Loader2Icon data-slot="spinner" role="status" aria-label={l("Loading", "불러오는 중")} className={cn("size-4 animate-spin", className)} {...props} />
  )
}

export { Spinner }
