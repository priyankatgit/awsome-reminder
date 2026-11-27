// Lucide icons rendered as inline SVG strings. Import each icon individually
// so the bundle only carries the ones used. Size and stroke come from CSS
// (.icon in style.css), never from here.
import {
  AlarmClock,
  Bell,
  Bold,
  CalendarClock,
  Check,
  Code,
  ChevronDown,
  ChevronRight,
  Download,
  Ellipsis,
  Eye,
  Heading2,
  ImagePlus,
  Inbox,
  Italic,
  Link,
  List,
  ListTodo,
  Mic,
  Pencil,
  Plus,
  Repeat,
  RepeatOff,
  Search,
  Settings,
  Square,
  SquareCheckBig,
  Trash2,
  TriangleAlert,
  X,
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
  close: render(X as IconNode),
  chevronDown: render(ChevronDown as IconNode),
  plus: render(Plus as IconNode),
  mic: render(Mic as IconNode),
  stop: render(Square as IconNode),
  download: render(Download as IconNode),
  // Description editor toolbar (Phase 1.6)
  bold: render(Bold as IconNode),
  italic: render(Italic as IconNode),
  heading: render(Heading2 as IconNode),
  list: render(List as IconNode),
  checklist: render(ListTodo as IconNode),
  code: render(Code as IconNode),
  link: render(Link as IconNode),
  image: render(ImagePlus as IconNode),
  preview: render(Eye as IconNode),
} as const;

export type IconName = keyof typeof icons;
