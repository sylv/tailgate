import {
  ArrowDownIcon,
  ArrowUpIcon,
  FloppyDiskIcon,
  LockSimpleIcon,
  PlusIcon,
  TrashIcon,
  XIcon,
} from "@phosphor-icons/react";
import type { FC, ReactNode } from "react";
import type { Effect, Rule, RuleError, Schema } from "../../api";
import { cn } from "../../lib/utils";
import { Button } from "../../ui/button";
import { IconButton } from "../../ui/icon-button";
import { Input } from "../../ui/input";
import { Segmented } from "../../ui/segmented";
import { Select } from "../../ui/select";
import { ExprEditor, highlight } from "../expr-editor";
import { ErrorText } from "../section";
import { effectText, effectTypes, placeholders } from "./effects";
import { Dot, FlowStep } from "./flow";

const modes = [
  { value: "on", label: "On" },
  { value: "log", label: "Log only" },
  { value: "off", label: "Off" },
] as const;

// filled means it acts, hollow means it only logs, grey means it's off
const dotFor = (r: Rule) =>
  r.mode === "off"
    ? "bg-fg-faint"
    : r.mode === "log"
      ? "border-[1.5px] border-success bg-surface"
      : "bg-success";

/** a rule as it reads: its name, condition and effects, with how it's running on the right */
export const RuleSummary: FC<{ rule: Rule; values: Set<string>; onOpen: () => void }> = ({
  rule,
  values,
  onOpen,
}) => (
  <FlowStep marker={<Dot className={dotFor(rule)} />}>
    <button
      onClick={onOpen}
      className={cn(
        "group -mx-3 mb-1 grid w-[calc(100%+1.5rem)] grid-cols-[minmax(0,1fr)_auto] gap-x-6 rounded-md px-3 py-2 text-left hover:bg-raised",
        rule.mode === "off" && "opacity-55",
      )}
    >
      <span className="min-w-0 truncate underline-offset-2 group-hover:underline">
        {rule.name || <span className="text-fg-faint">Unnamed rule</span>}
      </span>
      <span className="flex gap-4 text-[13px] text-fg-muted tabular-nums">
        {rule.mode !== "on" && <span>{modes.find((m) => m.value === rule.mode)?.label}</span>}
      </span>
      <Code values={values}>{rule.condition || "true"}</Code>
      {rule.effects.map((e, i) => (
        <Code key={i} values={values} arrow>
          {effectText(e)}
        </Code>
      ))}
    </button>
  </FlowStep>
);

const Code: FC<{ values: Set<string>; arrow?: boolean; children: string }> = ({
  values,
  arrow,
  children,
}) => (
  <code className="col-span-2 mt-0.5 block truncate font-mono text-[13px] text-fg-muted">
    {arrow && <span className="mr-2 text-fg-faint">→</span>}
    {highlight(children, values)}
  </code>
);

/** a built-in step of the auth section, read only */
export const LockedRule: FC<{ name: string; condition: string; effect: string; values: Set<string> }> = ({
  name,
  condition,
  effect,
  values,
}) => (
  <FlowStep
    marker={
      <span className="mt-2.5 flex size-[18px] items-center justify-center rounded-full bg-surface text-fg-faint">
        <LockSimpleIcon className="size-3.5" />
      </span>
    }
  >
    <div className="mb-1 grid grid-cols-[minmax(0,1fr)_auto] gap-x-6 py-2">
      <span>{name}</span>
      <span className="text-[13px] text-fg-faint">Built in</span>
      <Code values={values}>{condition}</Code>
      <Code values={values} arrow>
        {effect}
      </Code>
    </div>
  </FlowStep>
);

export const RuleEditor: FC<{
  rule: Rule;
  schema: Schema;
  errors: RuleError[];
  first: boolean;
  last: boolean;
  onChange: (r: Partial<Rule>) => void;
  onMove: (by: -1 | 1) => void;
  onRemove: () => void;
  onClose: () => void;
  /** it differs from what's saved, so it offers to save rather than close */
  changed: boolean;
  saving: boolean;
  onSave: () => void;
}> = ({
  rule,
  schema,
  errors,
  first,
  last,
  onChange,
  onMove,
  onRemove,
  onClose,
  changed,
  saving,
  onSave,
}) => {
  const err = (field: string) => errors.find((e) => e.field === field);
  const setEffect = (i: number, e: Partial<Effect>) =>
    onChange({ effects: rule.effects.map((x, j) => (j === i ? { ...x, ...e } : x)) });
  const other = errors.filter((e) => !/^(condition|effects\.\d+)$/.test(e.field));

  return (
    <FlowStep marker={<Dot className={dotFor(rule)} />}>
      <div className="-mx-3 mb-2 flex animate-enter flex-col gap-3 rounded-md border border-line bg-page/40 p-3">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            className="h-8 max-w-sm flex-1 basis-48 font-medium"
            placeholder="Name"
            aria-label="Name"
            aria-invalid={!!err("name") || undefined}
            value={rule.name}
            onChange={(e) => onChange({ name: e.target.value })}
          />
          <Segmented
            label="Mode"
            value={rule.mode}
            options={[...modes]}
            onValueChange={(mode) => onChange({ mode })}
          />
          <div className="ml-auto flex">
            <IconButton label="Move up" size="sm" disabled={first} onClick={() => onMove(-1)}>
              <ArrowUpIcon />
            </IconButton>
            <IconButton label="Move down" size="sm" disabled={last} onClick={() => onMove(1)}>
              <ArrowDownIcon />
            </IconButton>
            <IconButton label="Remove rule" size="sm" className="hover:text-danger" onClick={onRemove}>
              <TrashIcon />
            </IconButton>
            {changed ? (
              <IconButton label="Save" size="sm" loading={saving} className="text-fg" onClick={onSave}>
                <FloppyDiskIcon />
              </IconButton>
            ) : (
              <IconButton label="Close" size="sm" onClick={onClose}>
                <XIcon />
              </IconButton>
            )}
          </div>
        </div>
        <Labelled label="When">
          <ExprEditor
            value={rule.condition}
            onChange={(condition) => onChange({ condition })}
            section={rule.section}
            condition
            schema={schema}
            error={err("condition")}
            placeholder={
              rule.section === "pre"
                ? 'ingress == "funnel" && country not in ["AU"]'
                : 'service.name == "lyra"'
            }
          />
        </Labelled>
        <Labelled label="Then">
          <div className="flex flex-col gap-2">
            {rule.effects.map((e, i) => (
              <div key={i} className="flex flex-wrap items-start gap-2">
                <Select
                  label="Effect"
                  value={e.type}
                  options={effectTypes}
                  onValueChange={(type) => setEffect(i, { type })}
                  className="w-48"
                />
                {e.type.endsWith("-header") && (
                  <Input
                    className="w-56 font-mono text-[13px]"
                    placeholder="Header"
                    aria-label="Header"
                    value={e.name ?? ""}
                    onChange={(ev) => setEffect(i, { name: ev.target.value })}
                  />
                )}
                {e.type === "redirect" && (
                  <Select
                    label="Status"
                    value={String(e.status || 302)}
                    options={["302", "301", "303", "307", "308"].map((s) => ({ value: s, label: s }))}
                    onValueChange={(s) => setEffect(i, { status: Number(s) })}
                    className="w-20"
                  />
                )}
                {e.type in placeholders ? (
                  <ExprEditor
                    value={e.value ?? ""}
                    onChange={(value) => setEffect(i, { value })}
                    section={rule.section}
                    condition={false}
                    schema={schema}
                    error={err(`effects.${i}`)}
                    placeholder={placeholders[e.type]}
                  />
                ) : (
                  <ErrorText className="flex-1 self-center">{err(`effects.${i}`)?.message}</ErrorText>
                )}
                {rule.effects.length > 1 && (
                  <IconButton
                    label="Remove effect"
                    size="md"
                    onClick={() => onChange({ effects: rule.effects.filter((_, j) => j !== i) })}
                  >
                    <XIcon />
                  </IconButton>
                )}
              </div>
            ))}
            <Button
              variant="link"
              size="sm"
              icon={<PlusIcon />}
              className="w-fit"
              onClick={() => onChange({ effects: [...rule.effects, { type: "set-request-header" }] })}
            >
              Add effect
            </Button>
          </div>
        </Labelled>
        {other.map((e) => (
          <ErrorText key={e.field + e.message}>{e.message}</ErrorText>
        ))}
      </div>
    </FlowStep>
  );
};

const Labelled: FC<{ label: string; children: ReactNode }> = ({ label, children }) => (
  <div className="grid gap-1.5 sm:grid-cols-[2.5rem_minmax(0,1fr)] sm:gap-3">
    <span className="text-[13px] text-fg-faint sm:pt-1.5">{label}</span>
    {children}
  </div>
);

export const AddRule: FC<{ onClick: () => void }> = ({ onClick }) => (
  // the whole step is the target, so the + on the line lights up with the text.
  // the button inside is for the keyboard, its click bubbles up here
  <FlowStep
    onClick={onClick}
    className="group/add cursor-pointer"
    marker={
      <span className="mt-2.5 flex size-[18px] items-center justify-center rounded-full border border-line-strong bg-surface text-fg-faint group-hover/add:border-fg group-hover/add:text-fg">
        <PlusIcon weight="bold" className="size-2.5" />
      </span>
    }
  >
    <Button
      variant="link"
      size="sm"
      className="mt-[5px] h-7! group-hover/add:text-fg group-hover/add:underline"
    >
      Add rule
    </Button>
  </FlowStep>
);
