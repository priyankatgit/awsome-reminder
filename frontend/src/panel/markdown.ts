// Markdown preview for the description: marked renders, DOMPurify sanitizes.
// Both libraries load through import() the first time a preview renders, so
// they sit in their own chunks and never weigh on the list window.
//
// Safety rules (spec "Description = Markdown"): raw HTML is dropped; links keep
// only http, https and mailto targets (the panel opens them through OpenURL,
// the webview never navigates); images show only stored attachments, written
// `att:<ULID>` in the source and served by Go at /att/<ULID>, so the webview
// never fetches an arbitrary URL.

const ULID = "[0-9A-HJKMNP-TV-Z]{26}";
const ATT_REF = new RegExp(`^att:(${ULID})$`);
const ATT_SRC = new RegExp(`^/att/${ULID}$`);
const SAFE_HREF = /^(https?:\/\/|mailto:)/i;

const ALLOWED_TAGS = [
  "a", "blockquote", "br", "code", "del", "em", "h1", "h2", "h3", "h4", "h5", "h6", "hr", "img", "input",
  "li", "ol", "p", "pre", "strong", "table", "tbody", "td", "th", "thead", "tr", "ul",
];
const ALLOWED_ATTR = ["href", "title", "src", "alt", "type", "checked", "disabled", "start", "align"];

type Render = (md: string) => string;
let loading: Promise<Render> | null = null;

function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

async function load(): Promise<Render> {
  const [{ Marked }, { default: createDOMPurify }] = await Promise.all([import("marked"), import("dompurify")]);
  const marked = new Marked({
    gfm: true,
    breaks: true,
    renderer: {
      html: () => "", // raw HTML is stripped, block and inline
      link(token) {
        // false falls back to marked's own link renderer
        return SAFE_HREF.test(token.href) ? false : this.parser.parseInline(token.tokens);
      },
      image({ href, title, text }) {
        const m = ATT_REF.exec(href);
        if (!m) return escapeHtml(text); // not an attachment: its alt text only
        const t = title ? ` title="${escapeHtml(title)}"` : "";
        return `<img src="/att/${m[1]}" alt="${escapeHtml(text)}"${t}>`;
      },
    },
  });

  // A private instance, so these hooks never affect anyone else.
  const purify = createDOMPurify(window);
  purify.addHook("uponSanitizeAttribute", (node, data) => {
    const v = data.attrValue.trim();
    if (data.attrName === "href" && !SAFE_HREF.test(v)) data.keepAttr = false;
    if (data.attrName === "src" && !(node.nodeName === "IMG" && ATT_SRC.test(v))) data.keepAttr = false;
    if (data.attrName === "type" && v.toLowerCase() !== "checkbox") data.keepAttr = false;
  });
  const config = { ALLOWED_TAGS, ALLOWED_ATTR, ALLOW_DATA_ATTR: false, ALLOW_ARIA_ATTR: false };

  taskStates = (md) => {
    const out: boolean[] = [];
    marked.walkTokens(marked.lexer(md), (t) => {
      if (t.type === "list_item" && t.task) out.push(t.checked === true);
    });
    return out;
  };
  return (md) => purify.sanitize(marked.parse(md, { async: false }), config);
}

/** Sanitized HTML for a description. The first call loads marked and DOMPurify. */
export async function renderMarkdown(md: string): Promise<string> {
  loading ??= load().catch((err) => {
    loading = null; // a failed chunk load can be retried
    throw err;
  });
  return (await loading)(md);
}

/** Checked state of every task-list item, in preview order; set once marked loads. */
let taskStates: ((md: string) => boolean[]) | null = null;

/**
 * Flips the index-th task-list item ([ ] ↔ [x]), counting items exactly as
 * the preview shows their checkboxes: marked's own lexer decides what is a
 * task (not raw HTML, not code, not a paragraph continuation). Each [ ]/[x]
 * in the source is tried in turn; the one whose flip changes only task
 * index is the item's checkbox. Returns md unchanged when there is no such
 * item, or when marked hasn't loaded yet (the preview loads it first).
 */
export function toggleChecklistLine(md: string, index: number): string {
  if (!taskStates) return md;
  const base = taskStates(md);
  if (index < 0 || index >= base.length) return md;
  for (const m of md.matchAll(/\[[ xX]\]/g)) {
    const at = m.index + 1;
    const next = md.slice(0, at) + (md[at] === " " ? "x" : " ") + md.slice(at + 1);
    const states = taskStates(next);
    if (states.length !== base.length) continue;
    const changed = states.flatMap((v, i) => (v !== base[i] ? [i] : []));
    if (changed.length === 1 && changed[0] === index) return next;
  }
  return md;
}
