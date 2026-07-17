// Единые ru-RU форматтеры (§9 правил дизайна MAIN). Pre-created Intl —
// переиспользуем вместо инлайновых toLocaleString по компонентам.

const dateFmt = new Intl.DateTimeFormat("ru-RU", {
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
});
const timeFmt = new Intl.DateTimeFormat("ru-RU", {
  hour: "2-digit",
  minute: "2-digit",
});
const numFmt = new Intl.NumberFormat("ru-RU");

function toDate(v?: string | Date | null): Date | null {
  if (!v) return null;
  const d = typeof v === "string" ? new Date(v) : v;
  return isNaN(d.getTime()) ? null : d;
}

/** dd.MM.yyyy или «—». */
export function formatDate(v?: string | Date | null): string {
  const d = toDate(v);
  return d ? dateFmt.format(d) : "—";
}

/** { date: dd.MM.yyyy, time: HH:mm } — для двухстрочного отображения (§9). */
export function formatDateTime(
  v?: string | Date | null,
): { date: string; time: string } | null {
  const d = toDate(v);
  return d ? { date: dateFmt.format(d), time: timeFmt.format(d) } : null;
}

/** Число в ru-RU (пробелы-разделители). */
export function formatNumber(n: number): string {
  return numFmt.format(n);
}
