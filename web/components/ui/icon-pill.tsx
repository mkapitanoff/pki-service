import { cn } from "@/lib/utils";

export type IconPillTone = "primary" | "success" | "warning" | "destructive" | "muted";

const TONE_CLASSES: Record<IconPillTone, string> = {
  primary: "bg-primary/10 text-primary",
  success: "bg-success/10 text-success",
  warning: "bg-warning/10 text-warning",
  destructive: "bg-destructive/10 text-destructive",
  muted: "bg-muted text-muted-foreground",
};

/**
 * IconPill — квадрат-пилл с цветной иконкой (§2 правил MAIN).
 * Универсальный приём для KPI, шапок секций, строк списков.
 * Размер по умолчанию 40×40 (h-10 w-10); переопределяется через className.
 */
export function IconPill({
  tone = "primary",
  className,
  children,
}: {
  tone?: IconPillTone;
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <span
      className={cn(
        "inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-lg",
        TONE_CLASSES[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}
