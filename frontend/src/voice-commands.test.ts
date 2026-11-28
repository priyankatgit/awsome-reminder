import { describe, expect, it } from "vitest";
import { parseSegment, type Op, type ParseOptions } from "./voice-commands";

const t = (text: string): Op => ({ kind: "text", text });
const B: Op = { kind: "boundary" };
const SAVE: Op = { kind: "save" };

describe("parseSegment", () => {
  const cases: [string, string, Op[], ParseOptions?][] = [
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
    ["curly apostrophe save", "Buy milk. That’s all.", [t("Buy milk"), SAVE]],
    ["ellipsis character save", "Save all…", [SAVE]],
    ["curly quotes around a save", "“Save all.”", [SAVE]],
    ["left curly quote in a save", "‘Thats it’", [SAVE]],
    // An unengaged wake box: chatter that ends "that's it" stays text.
    ["strict: that's it is text", "blah blah. That's it.", [t("blah blah. That's it")], { strictSave: true }],
    ["strict: that's all is text", "That's all.", [t("That's all")], { strictSave: true }],
    ["strict: save them is text", "Save them.", [t("Save them")], { strictSave: true }],
    ["strict: save all saves", "Buy milk. Save all.", [t("Buy milk"), SAVE], { strictSave: true }],
    ["strict: save tasks saves", "save tasks", [SAVE], { strictSave: true }],
    ["strict: save them all saves", "Save them all!", [SAVE], { strictSave: true }],
    ["strict: cancel still cancels", "Buy milk. Never mind.", [t("Buy milk"), { kind: "cancel" }], { strictSave: true }],
    ["strict: cancel all still cancels", "cancel all", [{ kind: "cancel" }], { strictSave: true }],
    ["strict: cancel still cancels alone", "Cancel.", [{ kind: "cancel" }], { strictSave: true }],
  ];

  for (const [name, input, ops, opts] of cases) {
    it(name, () => {
      expect(parseSegment(input, opts)).toEqual(ops);
    });
  }
});
