// Lucide icons rendered as inline SVG strings. Import each icon individually
// so the bundle only carries the ones used. Size and stroke come from CSS
// (.icon in style.css), never from here.
import {
  AlarmClock,
  Bell,
  CalendarClock,
  Check,
  ChevronRight,
  Ellipsis,
  Inbox,
  Pencil,
  Repeat,
  RepeatOff,
  Search,
  Settings,
  SquareCheckBig,
  Trash2,
  TriangleAlert,
} from "lucide";

type IconNode = [tag: string, attrs: Record<string, string | number | undefined>][];

function attr(v: string | number): string {
  return String(v).replace(/&/g, "&amp;").replace(/"/g, "&quot;");
}

function render(node: IconNode): string {
  const body = node
    .map(([tag, attrs]) => {
      const a = Object.entries(attrs)
        .filter(([, v]) => v !== undefined)
        .map(([k, v]) => `${k}="${attr(v as string | number)}"`)
        .join(" ");
      return `<${tag} ${a}/>`;
    })
    .join("");
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" class="icon" viewBox="0 0 24 24" fill="none" ` +
    `stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${body}</svg>`
  );
}

export const icons = {
  search: render(Search as IconNode),
  bell: render(Bell as IconNode),
  todo: render(SquareCheckBig as IconNode),
  warning: render(TriangleAlert as IconNode),
  calendar: render(CalendarClock as IconNode),
  settings: render(Settings as IconNode),
  inbox: render(Inbox as IconNode),
  more: render(Ellipsis as IconNode),
  check: render(Check as IconNode),
  chevron: render(ChevronRight as IconNode),
  repeat: render(Repeat as IconNode),
  repeatOff: render(RepeatOff as IconNode),
  snooze: render(AlarmClock as IconNode),
  edit: render(Pencil as IconNode),
  trash: render(Trash2 as IconNode),
} as const;

export type IconName = keyof typeof icons;
