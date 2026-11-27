// The description editor of the detail panel: a Markdown textarea with a
// helper toolbar, an Edit | Preview toggle, inline images (paste, drop or the
// Image button) and an image lightbox. The panel owns the description's
// autosaver; every change here goes through it.
//
// Safety: the preview HTML comes from renderMarkdown (sanitized, attachments
// only); links open through OpenURL and the webview never navigates (click,
// middle-click, drag and context menu on links are all stopped here).
import { api, errorText } from "../api";
import { el } from "../dom";
import { icons } from "../icons";
import type { Autosaver } from "./autosave";
import * as fmt from "./format";
import { ACCEPT, checkImageType, checkInputSize, compressImage } from "./images";
import { renderMarkdown, toggleChecklistLine } from "./markdown";

export interface EditorOptions {
  /** id of the visible label that names the textarea. */
  labelledBy: string;
  /** The description's autosaver (shared with the panel's flush on switch/close). */
  saver: Autosaver<string>;
  /** The item whose description is on show; null when there is none. */
  itemId(): string | null;
}

export interface DescriptionEditor {
  /** Shows md; switched means a new item (its image errors are cleared). */
  fill(md: string, switched: boolean): void;
}

type Mode = "edit" | "preview";

interface Tool {
  icon: string;
  label: string;
  /** The Markdown it inserts, shown in the tooltip. */
  md: string;
  /** Ctrl+<key> (a letter) or Ctrl+Shift+<code>, matched below. */
  shortcut?: { shift: boolean; key: string };
  run(): void;
}

let mounted = 0;
/** Per page load, so a placeholder left over from an earlier session never matches a new one. */
const SESSION = Date.now().toString(36) + Math.random().toString(36).slice(2, 6);

/** A letter key by its character (any Latin layout), else by its position. */
function keyName(e: KeyboardEvent): string {
  if (/^[a-z]$/i.test(e.key)) return e.key.toLowerCase();
  if (/^Key[A-Z]$/.test(e.code)) return e.code.slice(3).toLowerCase();
  return e.code; // Digit8, Digit9: layouts put different symbols there
}

const hasFiles = (e: DragEvent) => e.dataTransfer?.types.includes("Files") ?? false;
const imagesOf = (files: FileList | null | undefined) => Array.from(files ?? []).filter((f) => f.type.startsWith("image/"));
const baseName = (name: string) => name.replace(/\.[^.]*$/, "");

export function mountEditor(host: HTMLElement, opts: EditorOptions): DescriptionEditor {
  const { saver, itemId } = opts;
  const uid = ++mounted;
  let mode: Mode = "edit";
  let renderSeq = 0;
  let uploadSeq = 0;

  // ---------- DOM ----------

  host.classList.add("md-editor");
  const ta = el("textarea", "input md-text");
  ta.id = `md-text-${uid}`;
  ta.setAttribute("aria-labelledby", opts.labelledBy);
  ta.placeholder = "Notes, steps, links (Markdown). Paste or drop images.";
  ta.spellcheck = false;

  const preview = el("div", "md-preview");
  preview.hidden = true;
  preview.setAttribute("aria-label", "Description preview");
  preview.setAttribute("role", "region");
  preview.tabIndex = -1; // takes focus back from a closed lightbox

  const error = el("p", "field-error md-error");
  error.setAttribute("role", "alert");
  error.hidden = true;

  const filePick = el("input");
  filePick.type = "file";
  filePick.accept = ACCEPT;
  filePick.multiple = true;
  filePick.hidden = true;
  filePick.tabIndex = -1;

  const state = (): fmt.TextState => ({ value: ta.value, selStart: ta.selectionStart, selEnd: ta.selectionEnd });
  const fmtTool = (run: (s: fmt.TextState) => fmt.TextState) => () => {
    if (mode !== "edit") return;
    const next = run(state());
    ta.focus();
    apply(next);
  };

  const tools: Tool[] = [
    { icon: icons.bold, label: "Bold", md: "**text**", shortcut: { shift: false, key: "b" }, run: fmtTool(fmt.bold) },
    { icon: icons.italic, label: "Italic", md: "*text*", shortcut: { shift: false, key: "i" }, run: fmtTool(fmt.italic) },
    { icon: icons.heading, label: "Heading", md: "## Heading", run: fmtTool(fmt.heading) },
    { icon: icons.list, label: "Bulleted list", md: "- item", shortcut: { shift: true, key: "Digit8" }, run: fmtTool(fmt.bulletList) },
    { icon: icons.checklist, label: "Checklist", md: "- [ ] item", shortcut: { shift: true, key: "Digit9" }, run: fmtTool(fmt.checklist) },
    { icon: icons.code, label: "Code", md: "`code`, or ``` around lines", shortcut: { shift: true, key: "c" }, run: fmtTool(fmt.code) },
    { icon: icons.link, label: "Link", md: "[text](https://)", shortcut: { shift: false, key: "k" }, run: fmtTool(fmt.link) },
    { icon: icons.image, label: "Image", md: "![name](att:…), or paste or drop one", run: () => mode === "edit" && filePick.click() },
  ];
  const shortcutLabel = (t: Tool) =>
    t.shortcut ? `Ctrl+${t.shortcut.shift ? "Shift+" : ""}${t.shortcut.key.replace("Digit", "").toUpperCase()}` : "";

  const toolbar = el("div", "md-toolbar");
  toolbar.setAttribute("role", "toolbar");
  toolbar.setAttribute("aria-label", "Description formatting");
  toolbar.setAttribute("aria-controls", ta.id);
  const toolButtons = tools.map((t, i) => {
    const b = el("button", "icon-btn md-tool");
    b.type = "button";
    b.innerHTML = t.icon;
    b.setAttribute("aria-label", t.label);
    const keys = shortcutLabel(t);
    if (keys) b.setAttribute("aria-keyshortcuts", keys.replace("Ctrl", "Control"));
    b.title = `${t.label}${keys ? ` (${keys})` : ""}\n${t.md}`;
    b.addEventListener("mousedown", (e) => e.preventDefault()); // keep the textarea's focus and selection
    b.addEventListener("click", t.run);
    if (i === 3 || i === 5) toolbar.append(el("span", "md-sep"));
    toolbar.append(b);
    return b;
  });

  const modes = el("div", "md-modes");
  modes.setAttribute("role", "group");
  modes.setAttribute("aria-label", "Description view");
  const modeBtn = (m: Mode, icon: string, label: string) => {
    const b = el("button", "md-mode");
    b.type = "button";
    b.innerHTML = `${icon}<span>${label}</span>`;
    b.addEventListener("click", () => setMode(m));
    modes.append(b);
    return b;
  };
  const editBtn = modeBtn("edit", icons.edit, "Edit");
  const previewBtn = modeBtn("preview", icons.preview, "Preview");
  toolbar.append(modes);

  host.replaceChildren(toolbar, ta, preview, error, filePick);

  // Roving focus: the toolbar is one Tab stop; arrows move inside it.
  const focusables = () => [...toolButtons, editBtn, previewBtn].filter((b) => !b.disabled);
  function rove(to: HTMLButtonElement): void {
    for (const b of focusables()) b.tabIndex = b === to ? 0 : -1;
  }
  toolbar.addEventListener("focusin", (e) => e.target instanceof HTMLButtonElement && rove(e.target));
  toolbar.addEventListener("keydown", (e) => {
    const list = focusables();
    const i = list.indexOf(e.target as HTMLButtonElement);
    if (i === -1) return;
    const to = { ArrowRight: i + 1, ArrowLeft: i - 1, Home: 0, End: list.length - 1 }[e.key];
    if (to === undefined) return;
    e.preventDefault();
    list[(to + list.length) % list.length].focus();
  });

  // ---------- editing ----------

  function showError(msg: string): void {
    error.textContent = msg;
    error.hidden = msg === "";
  }

  function changed(): void {
    const id = itemId();
    if (id) saver.change(id, ta.value);
  }

  /**
   * Puts next into the textarea. While it has focus only the changed span is
   * replaced, through the editing command, so Ctrl+Z undoes a toolbar action.
   */
  function apply(next: fmt.TextState): void {
    const prev = ta.value;
    if (prev !== next.value && document.activeElement === ta) {
      let a = 0;
      while (a < prev.length && a < next.value.length && prev[a] === next.value[a]) a++;
      let b = 0;
      const room = Math.min(prev.length, next.value.length) - a;
      while (b < room && prev[prev.length - 1 - b] === next.value[next.value.length - 1 - b]) b++;
      ta.setSelectionRange(a, prev.length - b);
      const text = next.value.slice(a, next.value.length - b);
      // execCommand is deprecated but is still the only undo-aware way in.
      const ok = text === "" ? document.execCommand("delete") : document.execCommand("insertText", false, text);
      if (!ok || ta.value !== next.value) ta.value = next.value;
    } else if (prev !== next.value) {
      ta.value = next.value;
    }
    ta.setSelectionRange(next.selStart, next.selEnd);
    if (prev !== next.value) changed();
  }

  ta.addEventListener("input", changed);
  ta.addEventListener("blur", () => void saver.flush());
  ta.addEventListener("keydown", (e) => {
    if (e.isComposing || e.altKey || !(e.ctrlKey || e.metaKey)) return;
    const k = keyName(e);
    const tool = tools.find((t) => t.shortcut && t.shortcut.shift === e.shiftKey && t.shortcut.key === k);
    if (!tool) return;
    e.preventDefault();
    tool.run();
  });

  // ---------- modes and preview ----------

  function setMode(m: Mode, focus = true): void {
    mode = m;
    const editing = m === "edit";
    ta.hidden = !editing;
    preview.hidden = editing;
    editBtn.setAttribute("aria-pressed", String(editing));
    previewBtn.setAttribute("aria-pressed", String(!editing));
    for (const b of toolButtons) b.disabled = !editing;
    rove(editing ? toolButtons[0] : previewBtn);
    if (editing) {
      renderSeq++; // drop a render in flight
      if (focus) ta.focus();
    } else {
      void saver.flush();
      void renderPreview();
    }
  }

  async function renderPreview(): Promise<void> {
    const my = ++renderSeq;
    const md = ta.value;
    if (md.trim() === "") {
      preview.replaceChildren(el("p", "md-empty", "Nothing to preview yet."));
      return;
    }
    let html: string;
    try {
      html = await renderMarkdown(md);
    } catch {
      if (my === renderSeq) showError("Couldn't load the preview");
      return;
    }
    if (my !== renderSeq || mode !== "preview") return;
    const boxes = () => Array.from(preview.querySelectorAll<HTMLInputElement>("input[type=checkbox]"));
    const focused = boxes().indexOf(document.activeElement as HTMLInputElement);
    preview.innerHTML = html; // sanitized by renderMarkdown
    for (const box of boxes()) box.removeAttribute("disabled");
    for (const a of Array.from(preview.querySelectorAll("a"))) {
      a.draggable = false;
      const href = a.getAttribute("href");
      if (href) a.title = href;
    }
    for (const img of Array.from(preview.querySelectorAll("img"))) {
      img.draggable = false;
      img.tabIndex = 0;
      img.setAttribute("aria-haspopup", "dialog");
      img.title = "Open image";
    }
    if (focused !== -1) boxes()[focused]?.focus();
  }

  preview.addEventListener("click", (e) => {
    const t = e.target as Element;
    const a = t.closest("a");
    if (a) {
      e.preventDefault(); // never navigate the webview, whatever the modifiers
      const href = a.getAttribute("href");
      if (href) api.openURL(href).then(() => showError(""), (err) => showError(`Couldn't open the link: ${errorText(err)}`));
      return;
    }
    const img = t.closest("img");
    if (img) openLightbox(img);
  });
  const stopOnLink = (e: Event) => (e.target as Element).closest("a") && e.preventDefault();
  preview.addEventListener("auxclick", stopOnLink); // middle-click would open a window
  preview.addEventListener("mousedown", (e) => e.button === 1 && e.preventDefault()); // no autoscroll
  preview.addEventListener("contextmenu", stopOnLink);
  preview.addEventListener("dragstart", (e) => e.preventDefault());
  preview.addEventListener("keydown", (e) => {
    const t = e.target as Element;
    if (t instanceof HTMLImageElement && (e.key === "Enter" || e.key === " ")) {
      e.preventDefault();
      openLightbox(t);
    }
  });
  preview.addEventListener("change", (e) => {
    const box = e.target;
    if (!(box instanceof HTMLInputElement) || box.type !== "checkbox") return;
    const index = Array.from(preview.querySelectorAll("input[type=checkbox]")).indexOf(box);
    const next = toggleChecklistLine(ta.value, index);
    if (next !== ta.value) {
      ta.value = next;
      changed();
      void saver.flush(); // a tick is a deliberate action: save it now
    }
    void renderPreview();
  });

  // ---------- images ----------

  /** Uploads one image behind an "Uploading…" placeholder at the caret. */
  async function upload(file: File): Promise<void> {
    const id = itemId();
    if (!id) return;
    const bad = checkImageType(file.type) ?? checkInputSize(file.size);
    if (bad) {
      showError(bad);
      return;
    }
    showError("");
    // The marker is unique, so it is found again wherever later edits move it.
    const marker = `![Uploading…](uploading:${SESSION}-${uid}-${++uploadSeq})`;
    apply(fmt.insertText(state(), marker));
    void saver.flush(); // a refresh before the debounce must not drop the placeholder
    let replacement = "";
    try {
      const att = await api.addAttachment(id, await compressImage(file));
      replacement = fmt.imageMarkdown(baseName(file.name), att.id);
    } catch (err) {
      if (itemId() === id) showError(`Couldn't add the image: ${errorText(err)}`);
    }
    await settle(id, marker, replacement);
  }

  /** Swaps the placeholder for the image (or removes it) in the item it was typed into. */
  async function settle(id: string, marker: string, replacement: string): Promise<void> {
    if (itemId() === id) {
      const next = fmt.replaceMarker(state(), marker, replacement);
      if (!next) return; // the user deleted the placeholder
      // A plain value set (caret kept), not an undoable edit: Ctrl+Z must
      // never bring back a placeholder that nothing will resolve.
      ta.value = next.value;
      ta.setSelectionRange(next.selStart, next.selEnd);
      changed();
      void saver.flush(); // so the stored image counts as referenced at once
      if (mode === "preview") void renderPreview();
      return;
    }
    // The panel moved on: patch the saved description of the original item.
    try {
      await saver.flush();
      const md = (await api.itemDetail(id)).item.description ?? "";
      if (!md.includes(marker)) return;
      saver.change(id, md.replace(marker, () => replacement));
      await saver.flush();
    } catch {
      // the item is gone; nothing to patch
    }
  }

  function addImages(files: File[]): void {
    for (const f of files) void upload(f);
  }

  filePick.addEventListener("change", () => {
    addImages(Array.from(filePick.files ?? []));
    filePick.value = "";
  });

  ta.addEventListener("paste", (e) => {
    const dt = e.clipboardData;
    const files = imagesOf(dt?.files);
    if (!dt || files.length === 0 || dt.types.includes("text/plain")) return; // text wins
    e.preventDefault();
    addImages(files);
  });

  // Files dragged over the editor are ours: stopPropagation keeps the Wails
  // runtime's document-level handler (which shows "no drop" in a window
  // without EnableFileDrop) from overriding the drop effect and the drop.
  const takeFiles = (e: DragEvent) => {
    if (!hasFiles(e) || mode !== "edit") return false;
    e.preventDefault();
    e.stopPropagation();
    e.dataTransfer!.dropEffect = "copy";
    return true;
  };
  host.addEventListener("dragenter", (e) => takeFiles(e) && host.classList.add("is-drop"));
  host.addEventListener("dragover", (e) => takeFiles(e) && host.classList.add("is-drop"));
  host.addEventListener("dragleave", (e) => {
    if (!host.contains(e.relatedTarget as Node | null)) host.classList.remove("is-drop");
  });
  host.addEventListener("drop", (e) => {
    host.classList.remove("is-drop");
    if (!takeFiles(e)) return;
    const files = imagesOf(e.dataTransfer?.files);
    if (files.length === 0) showError(checkImageType("") ?? "");
    else addImages(files);
  });
  // Elsewhere, the runtime stops file drops and main.ts stops link drops.

  // ---------- lightbox ----------

  function openLightbox(img: HTMLImageElement): void {
    if (!img.getAttribute("src")) return;
    const back = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const box = el("div", "lightbox");
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-modal", "true");
    box.setAttribute("aria-label", img.alt ? `Image: ${img.alt}` : "Image");
    const big = el("img", "lightbox-img");
    big.src = img.src;
    big.alt = img.alt;
    big.draggable = false;
    const close = el("button", "icon-btn lightbox-close");
    close.type = "button";
    close.innerHTML = icons.close;
    close.setAttribute("aria-label", "Close image");
    close.title = "Close (Esc)";
    box.append(big, close);

    const shut = () => {
      box.remove();
      (back?.isConnected ? back : preview).focus();
    };
    box.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation(); // the panel and list keep their Esc
        shut();
      } else if (e.key === "Tab") {
        e.preventDefault(); // the close button is the only stop: focus stays here
        close.focus();
      } else {
        e.stopPropagation(); // no list shortcuts behind the overlay
      }
    });
    box.addEventListener("click", (e) => e.target !== big && shut());
    document.body.append(box);
    close.focus();
  }

  // ---------- public ----------

  setMode("edit", false);
  // Load marked and DOMPurify now that a panel has opened, so the first
  // Preview is instant.
  void renderMarkdown("").catch(() => {});

  return {
    fill(md, switched) {
      if (ta.value !== md) ta.value = md;
      if (switched) {
        showError("");
        ta.scrollTop = 0;
        preview.scrollTop = 0;
      }
      if (mode === "preview") void renderPreview();
    },
  };
}
