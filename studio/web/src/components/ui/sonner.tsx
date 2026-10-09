import { Toaster as Sonner, type ToasterProps } from "sonner"
import {
  CircleCheckIcon,
  InfoIcon,
  TriangleAlertIcon,
  OctagonXIcon,
  Loader2Icon,
} from "lucide-react"

import { useThemeDark } from "@/components/studio/theme"

// Studio's theme is the "dark" class on <html> (theme.tsx): the toaster
// follows it (useThemeDark), and its colours are the palette's
// tokens (styles.css, src/lib/palette.ts). No hotkey: the toaster never
// takes focus or a key from the page (plan H5).
const Toaster = ({ ...props }: ToasterProps) => {
  const dark = useThemeDark()

  return (
    <Sonner
      theme={dark ? "dark" : "light"}
      hotkey={[]}
      className="toaster group"
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as React.CSSProperties
      }
      toastOptions={{
        classNames: {
          toast: "cn-toast",
        },
      }}
      {...props}
    />
  )
}

export { Toaster }
