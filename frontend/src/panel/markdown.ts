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

// A task-list item as marked sees it: optional blockquote markers and indent,
// a bullet or ordered marker, then [ ], [x] or [X] followed by text.
const TASK = /^((?:[ \t]*>)*[ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]+\[)([ xX])(\][ \t]+\S)/;
const FENCE = /^(?:[ \t]*>)*[ \t]*(`{3,}|~{3,})/;

/**
 * Flips the index-th task-list item ([ ] ↔ [x]), counting items in the order
 * the preview shows their checkboxes; lines inside fenced code don't count.
 * Returns md unchanged when there is no such item.
 */
export function toggleChecklistLine(md: string, index: number): string {
  const lines = md.split("\n");
  let fence: string | null = null; // the open fence's marker run
  let n = 0;
  for (let i = 0; i < lines.length; i++) {
    const f = FENCE.exec(lines[i]);
    if (fence) {
      if (f && f[1][0] === fence[0] && f[1].length >= fence.length) fence = null;
      continue;
    }
    if (f) {
      fence = f[1];
      continue;
    }
    const m = TASK.exec(lines[i]);
    if (!m) continue;
    if (n++ === index) {
      lines[i] = lines[i].replace(TASK, (_all, pre: string, mark: string, post: string) => pre + (mark === " " ? "x" : " ") + post);
      return lines.join("\n");
    }
  }
  return md;
}
