import { describe, expect, it } from "vitest";
import { parseSegment, type Op } from "./voice-commands";

const t = (text: string): Op => ({ kind: "text", text });
const B: Op = { kind: "boundary" };
const SAVE: Op = { kind: "save" };

describe("parseSegment", () => {
  const cases: [string, string, Op[], { multi?: boolean }?][] = [
    ["plain text", "Call Sam tomorrow at 10.", [t("Call Sam tomorrow at 10")]],
    ["boundary between sentences", "Call Sam. New task. Buy milk", [t("Call Sam"), B, t("Buy milk")]],
    [
      "boundary variants with and/okay",
      "call Sam and create a new task okay buy milk next task",
      [t("call Sam"), B, t("buy milk"), B],
    ],
    ["boundary alone", "New task.", [B]],
    ["add another task", "Buy milk, add another task, eggs", [t("Buy milk"), B, t("eggs")]],
    ["save alone", "Save all.", [SAVE]],
    ["save as last sentence", "Buy milk. That's all.", [t("Buy milk"), SAVE]],
    ["save mid-sentence is text", "remind me to save all the logs", [t("remind me to save all the logs")]],
    ["cancel prefix is text", "Cancel the dentist appointment.", [t("Cancel the dentist appointment")]],
    ["scratch", "Scratch that.", [{ kind: "scratch" }]],
    ["delete that", "delete that", [{ kind: "scratch" }]],
    ["never mind", "Never mind", [{ kind: "cancel" }]],
    ["cancel all", "cancel all.", [{ kind: "cancel" }]],
    ["boundary prefix accepted cost", "new task template for sprint", [B, t("template for sprint")]],
    ["whole words only", "Renew task list", [t("Renew task list")]],
    ["extra spaces and case", "  NEW   TASK  ", [B]],
    ["empty", "   ", []],
    [
      "reply mode keeps boundary and save as text",
      "Thanks, new task. Save all.",
      [t("Thanks, new task. Save all")],
      { multi: false },
    ],
    ["reply mode still scratches", "Thanks. Scratch that.", [t("Thanks"), { kind: "scratch" }], { multi: false }],
    ["reply mode cancel is text", "never mind", [t("never mind")], { multi: false }],
  ];

  for (const [name, input, ops, opts] of cases) {
    it(name, () => {
      expect(parseSegment(input, opts)).toEqual(ops);
    });
  }
});
