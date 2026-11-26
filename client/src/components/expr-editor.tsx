import { useEffect, useRef, useState, type ReactNode } from "react";
import { api, type RuleError, type Schema } from "../api";
import { cn } from "../lib/utils";

// prettier-ignore
const countries = "AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW".split(" ");
const countryNames = new Intl.DisplayNames(["en"], { type: "region" });

const keywords = new Set([
  "and",
  "or",
  "not",
  "in",
  "matches",
  "contains",
  "startsWith",
  "endsWith",
  "let",
  "true",
  "false",
  "nil",
]);

const tokenRe =
  /("(?:[^"\\\n]|\\.)*"?|'(?:[^'\\\n]|\\.)*'?|`[^`]*`?)|(\/\/[^\n]*|\/\*[\s\S]*?(?:\*\/|$))|(\d+(?:\.\d+)?)|([A-Za-z_$#][\w$]*(?:\.[A-Za-z_]\w*)*)|(\s+)|([\s\S])/g;

function tokenClass(m: RegExpMatchArray, values: Set<string>) {
  if (m[1]) return "text-code-string";
  if (m[2]) return "text-fg-faint italic";
  if (m[3]) return "text-code-number";
  if (m[4]) {
    if (keywords.has(m[4])) return "text-code-keyword";
    if (values.has(m[4])) return "text-code-value";
    return "";
  }
  if (m[6]) return "text-fg-muted";
  return "";
}

function offsetOf(src: string, line: number, column: number) {
  const lines = src.split("\n");
  return lines.slice(0, line - 1).reduce((n, l) => n + l.length + 1, 0) + column - 1;
}

// highlight colours each token, underlines the one with the error and puts a
// marker at the caret so the completions can sit under it
export function highlight(
  src: string,
  values: Set<string>,
  errorAt = -1,
  caretAt = -1,
  caret?: React.Ref<HTMLSpanElement>,
) {
  const out: ReactNode[] = [];
  const marker = () => <span key="caret" ref={caret} />;
  for (const m of src.matchAll(tokenRe)) {
    const start = m.index;
    const text = m[0];
    const end = start + text.length;
    let cls = tokenClass(m, values);
    if (errorAt >= start && errorAt < end && !m[5]) cls += " underline decoration-danger decoration-wavy";
    if (caretAt > start && caretAt < end) {
      out.push(
        <span key={start} className={cls}>
          {text.slice(0, caretAt - start)}
        </span>,
        marker(),
        <span key={start + "b"} className={cls}>
          {text.slice(caretAt - start)}
        </span>,
      );
      continue;
    }
    if (caretAt === start) out.push(marker());
    out.push(
      <span key={start} className={cls}>
        {text}
      </span>,
    );
  }
  if (caretAt === src.length) out.push(marker());
  return out;
}

interface Item {
  label: string;
  insert: string;
  detail: string;
}

// completions for the word or string before the caret. Inside a string after
// service.name, ingress or country it suggests the values those can have
function complete(text: string, caret: number, schema: Schema, section: string) {
  const before = text.slice(0, caret);
  const tokens = [...before.matchAll(tokenRe)];
  const last = tokens.at(-1);
  const open = last?.[1] && last[0].startsWith('"') && (last[0].length === 1 || !last[0].endsWith('"'));
  if (last && open) {
    const from = last.index + 1;
    const prefix = before.slice(from).toLowerCase();
    const ident = /([\w.]+)\s*(?:==|!=|(?:not\s+)?in\s*\[[^\]]*?)\s*$/.exec(before.slice(0, last.index))?.[1];
    const options: Record<string, [string, string][]> = {
      "service.name": schema.services.map((s) => [s, "service"]),
      ingress: [
        ["tailnet", ""],
        ["funnel", ""],
      ],
      country: countries.map((c) => [c, countryNames.of(c) ?? ""]),
      "user.via": [
        ["session", ""],
        ["key", "a key header"],
        ["tailnet", "a linked login"],
      ],
      method: ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"].map((m) => [m, ""]),
    };
    const close = text[caret] === '"' ? "" : '"';
    const items = (options[ident ?? ""] ?? [])
      .filter(
        ([v, d]) =>
          v.toLowerCase().startsWith(prefix) || (prefix.length > 1 && d.toLowerCase().startsWith(prefix)),
      )
      .map(([v, d]) => ({ label: v, insert: v + close, detail: d }));
    return { from, items };
  }
  const word = /[A-Za-z_][\w.]*$/.exec(before);
  if (!word || (last && (last[2] || last[3]))) return { from: caret, items: [] };
  const w = word[0].toLowerCase();
  const items = schema.values
    .filter((v) => (section === "post" || v.pre) && v.name.toLowerCase().startsWith(w) && v.name !== word[0])
    .map((v) => ({
      label: v.name,
      insert: v.name + (v.type.startsWith("func") ? "(" : ""),
      detail: `${v.type}, ${v.desc}`,
    }));
  return { from: word.index, items };
}

export function ExprEditor({
  value,
  onChange,
  section,
  condition,
  schema,
  error,
  placeholder,
}: {
  value: string;
  onChange: (v: string) => void;
  section: "pre" | "post";
  // a condition is checked as a bool, anything else as a string
  condition: boolean;
  schema: Schema;
  // error is from the last save
  error?: RuleError;
  placeholder?: string;
}) {
  const area = useRef<HTMLTextAreaElement>(null);
  const back = useRef<HTMLPreElement>(null);
  const caret = useRef<HTMLSpanElement>(null);
  const [problem, setProblem] = useState<RuleError | null>(null);
  const [cursor, setCursor] = useState(-1);
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState(0);
  const [pos, setPos] = useState({ left: 0, top: 0 });
  const values = new Set(schema.values.map((v) => v.name));

  useEffect(() => {
    setProblem(null);
    if (!value.trim()) return;
    let stale = false;
    const t = setTimeout(() => {
      api<{ problem: RuleError | null }>("POST", "/api/admin/rules/check", {
        section,
        expr: value,
        condition,
      }).then(
        (r) => !stale && setProblem(r.problem),
        () => {},
      );
    }, 250);
    return () => {
      stale = true;
      clearTimeout(t);
    };
  }, [value, section, condition]);

  const shown = problem ?? (error && error.message ? error : null);
  const errorAt = shown?.line && shown.column ? offsetOf(value, shown.line, shown.column) : -1;
  const { from, items } =
    open && cursor >= 0 ? complete(value, cursor, schema, section) : { from: 0, items: [] };
  const list = items.slice(0, 8);

  useEffect(() => {
    if (caret.current && back.current) {
      setPos({ left: caret.current.offsetLeft, top: caret.current.offsetTop - back.current.scrollTop });
    }
  }, [cursor, value, open]);

  function accept(item: Item) {
    const next = value.slice(0, from) + item.insert + value.slice(cursor);
    const at = from + item.insert.length;
    onChange(next);
    setOpen(false);
    requestAnimationFrame(() => {
      area.current?.setSelectionRange(at, at);
      setCursor(at);
    });
  }

  const layer = "px-3 py-[5px] font-mono text-[13px] leading-5 whitespace-pre-wrap break-words";
  return (
    <div className="flex min-w-0 flex-1 basis-64 flex-col gap-1">
      <div
        className={cn(
          "relative min-w-0 flex-1 rounded-md border focus-within:ring-1 focus-within:ring-inset",
          shown
            ? "border-danger focus-within:ring-danger"
            : "border-line hover:border-line-strong focus-within:border-brand-bright focus-within:ring-brand-bright",
        )}
      >
        <pre
          ref={back}
          aria-hidden
          className={`pointer-events-none absolute inset-0 overflow-hidden ${layer}`}
        >
          {highlight(value, values, errorAt, cursor, caret)}
          {"\n"}
        </pre>
        <textarea
          ref={area}
          value={value}
          placeholder={placeholder}
          rows={Math.max(1, value.split("\n").length)}
          spellCheck={false}
          autoCapitalize="off"
          autoComplete="off"
          className={`relative block w-full resize-none bg-transparent text-transparent caret-fg outline-none placeholder:text-fg-faint ${layer}`}
          onChange={(e) => {
            onChange(e.target.value);
            setCursor(e.target.selectionStart);
            setOpen(true);
            setSelected(0);
          }}
          onSelect={(e) => setCursor(e.currentTarget.selectionStart)}
          onScroll={(e) => {
            if (back.current) back.current.scrollTop = e.currentTarget.scrollTop;
          }}
          onBlur={() => setOpen(false)}
          onKeyDown={(e) => {
            if (!list.length) return;
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              setSelected((s) => (s + (e.key === "ArrowDown" ? 1 : list.length - 1)) % list.length);
            } else if (e.key === "Enter" || e.key === "Tab") {
              e.preventDefault();
              accept(list[Math.min(selected, list.length - 1)]);
            } else if (e.key === "Escape") {
              setOpen(false);
            }
          }}
        />
        {list.length > 0 && (
          <ul
            className="absolute z-10 mt-5 w-80 max-w-[calc(100vw-2rem)] animate-pop-in overflow-hidden rounded-lg border border-line bg-raised p-1 text-sm shadow-pop"
            style={{ left: Math.min(pos.left, 240), top: pos.top + 8 }}
          >
            {list.map((item, i) => (
              <li
                key={item.label}
                // mousedown so the textarea doesn't blur first
                onMouseDown={(e) => {
                  e.preventDefault();
                  accept(item);
                }}
                className={cn("cursor-pointer rounded-md px-2 py-1.5", i === selected && "bg-hover")}
              >
                <div className="font-mono text-[13px]">{item.label}</div>
                {i === selected && item.detail && <div className="text-xs text-fg-muted">{item.detail}</div>}
              </li>
            ))}
          </ul>
        )}
      </div>
      {shown && (
        <p className="text-xs text-danger">
          {shown.line ? `${shown.line}:${shown.column} ` : ""}
          {shown.message}
        </p>
      )}
    </div>
  );
}
