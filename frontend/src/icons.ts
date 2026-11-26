// Lucide icons rendered as inline SVG strings. Import each icon individually
// so the bundle only carries the ones used. Size and stroke come from CSS
// (.icon in style.css), never from here.
import { Bell, CalendarClock, Search, SquareCheckBig, TriangleAlert } from "lucide";

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
} as const;

export type IconName = keyof typeof icons;
