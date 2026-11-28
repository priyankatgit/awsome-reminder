import { beforeEach, describe, expect, it, vi } from "vitest";

const h = vi.hoisted(() => {
  window.matchMedia = ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never;
  return {
    info: undefined as unknown as () => Promise<unknown>,
    setToken: [] as string[],
    forgot: 0,
    tests: 0,
    newTopic: undefined as unknown as () => Promise<unknown>,
    emit: (() => {}) as (s: unknown) => void,
  };
});
vi.mock("./api", () => ({
  api: {
    push: {
      info: () => h.info(),
      setToken: async (t: string) => void h.setToken.push(t),
      forgetToken: async () => void h.forgot++,
      test: async () => void h.tests++,
      newTopic: () => h.newTopic(),
    },
    openURL: async () => {},
  },
  errorText: String,
  onPushStatus: (cb: (s: unknown) => void) => {
    h.emit = cb;
    return () => {};
  },
}));

import { graceLabel, mountPushSection, pushStatusLine, type PushDraft, type PushForm } from "./push-settings";

const NOW = new Date("2026-11-28T10:00:00Z");
const st = (state: string, extra: Record<string, unknown> = {}) => ({
  state, scheduled: 0, lastSent: "", message: "", sentToday: 0, ...extra,
});

describe("pushStatusLine", () => {
  it("says off", () => {
    expect(pushStatusLine(st("off") as never, NOW)).toBe("Phone push is off");
  });
  it("counts scheduled alerts and says when the last was sent", () => {
    const two = new Date(NOW.getTime() - 2 * 60_000).toISOString();
    expect(pushStatusLine(st("ok", { scheduled: 4, lastSent: two }) as never, NOW)).toBe("Phone push: 4 scheduled · last sent 2 min ago");
  });
  it("leaves out the last-sent part when nothing was sent", () => {
    expect(pushStatusLine(st("ok", { scheduled: 1, lastSent: "0001-01-01T00:00:00Z" }) as never, NOW)).toBe("Phone push: 1 scheduled");
  });
  it("shows the message for auth, quota and error", () => {
    expect(pushStatusLine(st("auth", { message: "The server refused the token" }) as never, NOW)).toBe("The server refused the token");
    expect(pushStatusLine(st("quota", { message: "Daily limit near" }) as never, NOW)).toBe("Daily limit near");
    expect(pushStatusLine(st("error", { message: "Boom" }) as never, NOW)).toBe("Boom");
  });
  it("says offline will retry", () => {
    expect(pushStatusLine(st("offline") as never, NOW)).toBe("Offline · will retry");
  });
});

describe("graceLabel", () => {
  it("names the options", () => {
    expect(graceLabel(0)).toBe("immediately");
    expect(graceLabel(120_000)).toBe("2 minutes");
    expect(graceLabel(60_000)).toBe("1 minute");
    expect(graceLabel(600_000)).toBe("10 minutes");
  });
});

const info = (extra: Record<string, unknown> = {}) => ({
  status: st("ok", { scheduled: 4 }),
  hasToken: false,
  tokenStore: "Windows Credential Manager",
  subscribeUrl: "https://ntfy.sh/ar-abc",
  qr: "data:image/png;base64,AAAA",
  ...extra,
});
const tick = () => new Promise((r) => setTimeout(r, 0));

function fakeForm(over: Partial<PushDraft> = {}) {
  const d: PushDraft = { enabled: false, server: "https://ntfy.sh", topic: "ar-abc", replies: true, hideText: false, graceSec: 120, ...over };
  const f: PushForm & { d: PushDraft } = {
    d,
    get: () => d,
    patch: (p) => void Object.assign(d, p),
    adoptSaved: () => {},
  };
  return f;
}
async function mount(form: ReturnType<typeof fakeForm>) {
  document.body.innerHTML = "<section id='h'></section>";
  const host = document.getElementById("h")!;
  const ctl = mountPushSection(host, form);
  await tick();
  ctl.render();
  await tick();
  return { host, ctl };
}
const q = <T extends HTMLElement>(host: HTMLElement, s: string) => host.querySelector<T>(s)!;
const button = (host: HTMLElement, text: string) =>
  [...host.querySelectorAll("button")].find((b) => b.textContent === text) as HTMLButtonElement;
const visible = (e: HTMLElement) => !e.closest("[hidden]");

describe("section", () => {
  beforeEach(() => {
    h.info = async () => info();
    h.setToken = [];
    h.forgot = 0;
    h.tests = 0;
  });

  it("hides the details while push is off", async () => {
    const { host } = await mount(fakeForm());
    expect(visible(q(host, "#push-details"))).toBe(false);
    expect(q<HTMLInputElement>(host, "#push-on").checked).toBe(false);
  });

  it("shows the details, the topic and the QR while push is on", async () => {
    const { host } = await mount(fakeForm({ enabled: true }));
    expect(visible(q(host, "#push-details"))).toBe(true);
    expect(q<HTMLInputElement>(host, "#push-topic").value).toBe("ar-abc");
    expect(q<HTMLImageElement>(host, ".push-qr").src).toContain("data:image/png");
    expect(host.textContent).toContain("https://ntfy.sh/ar-abc");
    expect(host.textContent).toContain("Phone push: 4 scheduled");
  });

  it("asks before switching on with the text not hidden, and Cancel reverts the switch", async () => {
    const form = fakeForm();
    const { host } = await mount(form);
    const sw = q<HTMLInputElement>(host, "#push-on");
    sw.checked = true;
    sw.dispatchEvent(new Event("change"));
    const note = q(host, "#push-consent");
    expect(visible(note)).toBe(true);
    expect(note.textContent).toContain("Reminder text will be sent to ntfy.sh.");
    expect(form.d.enabled).toBe(false);
    button(host, "Cancel").click();
    expect(sw.checked).toBe(false);
    expect(visible(note)).toBe(false);
    expect(form.d.enabled).toBe(false);
  });

  it("Turn on enables push in the form", async () => {
    const form = fakeForm();
    const { host } = await mount(form);
    const sw = q<HTMLInputElement>(host, "#push-on");
    sw.checked = true;
    sw.dispatchEvent(new Event("change"));
    button(host, "Turn on").click();
    expect(form.d.enabled).toBe(true);
    expect(visible(q(host, "#push-consent"))).toBe(false);
    expect(visible(q(host, "#push-details"))).toBe(true);
  });

  it("switches on without a note when the text is hidden", async () => {
    const form = fakeForm({ hideText: true });
    const { host } = await mount(form);
    const sw = q<HTMLInputElement>(host, "#push-on");
    sw.checked = true;
    sw.dispatchEvent(new Event("change"));
    expect(visible(q(host, "#push-consent"))).toBe(false);
    expect(form.d.enabled).toBe(true);
  });

  it("asks before un-hiding the text while push is on, and Cancel re-ticks Hide text", async () => {
    const form = fakeForm({ enabled: true, hideText: true });
    const { host } = await mount(form);
    const hide = q<HTMLInputElement>(host, "#push-hide");
    hide.checked = false;
    hide.dispatchEvent(new Event("change"));
    const note = q(host, "#push-consent");
    expect(visible(note)).toBe(true);
    expect(note.textContent).toContain("Reminder text will be sent to ntfy.sh.");
    expect(form.d.hideText).toBe(true);
    button(host, "Cancel").click();
    expect(hide.checked).toBe(true);
    expect(form.d.hideText).toBe(true);
    hide.checked = false;
    hide.dispatchEvent(new Event("change"));
    button(host, "Send text").click();
    expect(form.d.hideText).toBe(false);
    expect(visible(note)).toBe(false);
  });

  it("shows why the push info could not be read", async () => {
    h.info = async () => {
      throw new Error("no connection");
    };
    const { host } = await mount(fakeForm({ enabled: true }));
    expect(host.textContent).toContain("no connection");
  });

  it("writes the grace choice into the form in seconds", async () => {
    const form = fakeForm({ enabled: true });
    const { host } = await mount(form);
    const sel = q<HTMLSelectElement>(host, "#push-grace");
    expect([...sel.options].map((o) => o.textContent)).toEqual(["immediately", "2 minutes", "5 minutes", "10 minutes"]);
    expect(sel.value).toBe("120");
    sel.value = "300";
    sel.dispatchEvent(new Event("change"));
    expect(form.d.graceSec).toBe(300);
  });

  it("saves the token at once and clears the field", async () => {
    const { host } = await mount(fakeForm({ enabled: true }));
    const tok = q<HTMLInputElement>(host, "#push-token");
    tok.value = "tk_secret";
    tok.dispatchEvent(new Event("input"));
    button(host, "Save").click();
    await tick();
    expect(h.setToken).toEqual(["tk_secret"]);
    expect(tok.value).toBe("");
  });

  it("shows where a saved token lives and forgets it", async () => {
    h.info = async () => info({ hasToken: true });
    const { host } = await mount(fakeForm({ enabled: true }));
    expect(host.textContent).toContain("stored in: Windows Credential Manager");
    button(host, "Forget").click();
    await tick();
    expect(h.forgot).toBe(1);
  });

  it("sends a test", async () => {
    const { host } = await mount(fakeForm({ enabled: true }));
    button(host, "Send test").click();
    await tick();
    expect(h.tests).toBe(1);
  });

  it("New topic adopts the saved settings", async () => {
    const form = fakeForm({ enabled: true });
    const adopted: unknown[] = [];
    form.adoptSaved = (s) => void adopted.push(s);
    h.newTopic = async () => ({ pushTopic: "ar-new" });
    const { host } = await mount(form);
    button(host, "New topic").click();
    await tick();
    expect(adopted).toEqual([{ pushTopic: "ar-new" }]);
  });

  it("follows live status events", async () => {
    const { host } = await mount(fakeForm({ enabled: true }));
    h.emit(st("offline"));
    expect(host.textContent).toContain("Offline · will retry");
  });
});
