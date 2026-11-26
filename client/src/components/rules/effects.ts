import type { Effect } from "../../api";

export const effectTypes = [
  { value: "block", label: "Block" },
  { value: "redirect", label: "Redirect" },
  { value: "skip", label: "Skip the rest of this section" },
  { value: "set-request-header", label: "Set request header" },
  { value: "remove-request-header", label: "Remove request header" },
  { value: "set-response-header", label: "Set response header" },
  { value: "remove-response-header", label: "Remove response header" },
];

export const placeholders: Record<string, string> = {
  block: '"Optional message"',
  redirect: '"https://" + host + "/new"',
  "set-request-header": "user.name",
  "set-response-header": '"max-age=31536000"',
};

/** an effect as one line of code, the way the rule reads */
export const effectText = (e: Effect) =>
  [e.type, e.type === "redirect" && e.status && e.status !== 302 ? String(e.status) : "", e.name, e.value]
    .filter(Boolean)
    .join(" ");
