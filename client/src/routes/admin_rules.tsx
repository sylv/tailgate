import { useState } from "react";
import { api, type Rule, type RuleError, type RuleList, type Schema } from "../api";
import { PageHeader } from "../components/admin-shell";
import { Note } from "../components/note";
import { Flow, Stage } from "../components/rules/flow";
import { AddRule, LockedRule, RuleEditor, RuleSummary } from "../components/rules/rule-item";
import { ErrorText } from "../components/section";
import { useLoad } from "../hooks/use-load";
import { Button } from "../ui/button";

type Draft = Rule & { key: string };

let nextKey = 0;
const draft = (r: Rule): Draft => ({ ...r, key: r.id || `new${nextKey++}` });
const strip = (rs: Draft[]): Rule[] =>
  rs.map(({ id, section, name, condition, effects, mode }) => ({
    id,
    section,
    name,
    condition,
    effects,
    mode,
  }));

export function RulesRoute() {
  const [list, error] = useLoad(() => api<RuleList>("GET", "/api/admin/rules"));
  const [schema] = useLoad(() => api<Schema>("GET", "/api/admin/rules/schema"));
  if (error) return <ErrorText>{error}</ErrorText>;
  if (!list || !schema) return null;
  return <Rules initial={list} schema={schema} />;
}

function Rules({ initial, schema }: { initial: RuleList; schema: Schema }) {
  const [saved, setSaved] = useState(initial);
  const [rules, setRules] = useState(() => initial.rules.map(draft));
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [errors, setErrors] = useState<Record<string, RuleError[]>>({});
  const [busy, setBusy] = useState(false);
  const [saveError, setSaveError] = useState("");
  const values = new Set(schema.values.map((v) => v.name));
  const ordered = [...rules.filter((r) => r.section === "pre"), ...rules.filter((r) => r.section === "post")];
  const dirty = JSON.stringify(strip(ordered)) !== JSON.stringify(saved.rules);

  const toggle = (key: string, on: boolean) =>
    setOpen((o) => {
      const next = new Set(o);
      if (on) next.add(key);
      else next.delete(key);
      return next;
    });

  function update(key: string, r: Partial<Rule>) {
    setRules((rs) => rs.map((x) => (x.key === key ? { ...x, ...r } : x)));
    setErrors((es) => Object.fromEntries(Object.entries(es).filter(([k]) => k !== key)));
  }

  function move(key: string, by: -1 | 1) {
    setRules((rs) => {
      const r = rs.find((x) => x.key === key)!;
      const same = rs.filter((x) => x.section === r.section);
      const i = same.indexOf(r);
      const j = i + by;
      if (j < 0 || j >= same.length) return rs;
      [same[i], same[j]] = [same[j], same[i]];
      return [...rs.filter((x) => x.section !== r.section), ...same];
    });
  }

  function add(section: "pre" | "post") {
    const r = draft({ id: "", section, name: "", condition: "", effects: [{ type: "block" }], mode: "on" });
    setRules((rs) => [...rs, r]);
    toggle(r.key, true);
  }

  async function save() {
    setBusy(true);
    setSaveError("");
    try {
      const res = await api<RuleList>("PUT", "/api/admin/rules", { rules: strip(ordered) });
      if (res.errors?.length) {
        const byKey: Record<string, RuleError[]> = {};
        for (const e of res.errors) (byKey[ordered[e.rule].key] ??= []).push(e);
        setErrors(byKey);
        setOpen((o) => new Set([...o, ...Object.keys(byKey)]));
        setSaveError(res.errors.length === 1 ? "A rule has a problem" : `${res.errors.length} problems`);
      } else {
        setSaved(res);
        setRules(res.rules.map(draft));
        setErrors({});
        setOpen(new Set());
      }
    } catch (err) {
      setSaveError((err as Error).message);
    }
    setBusy(false);
  }

  const section = (s: "pre" | "post") => {
    const these = ordered.filter((r) => r.section === s);
    return (
      <>
        {these.map((r, i) =>
          open.has(r.key) ? (
            <RuleEditor
              key={r.key}
              rule={r}
              schema={schema}
              errors={errors[r.key] ?? []}
              first={i === 0}
              last={i === these.length - 1}
              onChange={(c) => update(r.key, c)}
              onMove={(by) => move(r.key, by)}
              onRemove={() => setRules((rs) => rs.filter((x) => x.key !== r.key))}
              onClose={() => toggle(r.key, false)}
              changed={
                JSON.stringify(strip([r])[0]) !== JSON.stringify(saved.rules.find((x) => x.id === r.id))
              }
              saving={busy}
              onSave={save}
            />
          ) : (
            <RuleSummary key={r.key} rule={r} values={values} onOpen={() => toggle(r.key, true)} />
          ),
        )}
        <AddRule onClick={() => add(s)} />
      </>
    );
  };

  return (
    <>
      <PageHeader
        title="Rules"
        description={
          <>
            Every app runs these from the top, and the first block or redirect ends the request. Conditions
            and values are{" "}
            <a
              href="https://expr-lang.org/docs/language-definition"
              className="text-fg underline-offset-2 hover:underline"
            >
              expr
            </a>
            , so text needs quotes
          </>
        }
      />
      <Note tone="info" className="mb-8">
        Over Funnel, rules before sign-in also run on tailgate's own pages, where app.id is "". On the tailnet
        they don't, so a bad rule can't lock you out of this page
      </Note>
      {saved.error && (
        <Note tone="danger" className="mb-8">
          The saved rules can't be loaded, so every request to an app is blocked until they're fixed.{" "}
          {saved.error}
        </Note>
      )}
      <Flow>
        <Stage title="A request comes in" />
        {section("pre")}
        <Stage title="Sign in" description="Sign-in rules can't be edited" className="mt-8" />
        <LockedRule
          name="Login"
          condition="!user.signedIn && !app.dangerouslyDisableAuth"
          effect="login"
          values={values}
        />
        <LockedRule
          name="Access"
          condition="!app.dangerouslyDisableAuth && !user.hasAccess"
          effect={`block "you do not have access to this app"`}
          values={values}
        />
        <Stage title="Signed in" className="mt-8" />
        {section("post")}
        <Stage title="On to the app" className="mt-8" />
      </Flow>
      {(dirty || saveError) && (
        // as wide as the page's content, so it lines up with what's above it
        <div className="fixed inset-x-0 bottom-6 z-50">
          <div className="mx-auto max-w-5xl px-4 md:px-8">
            <div className="flex animate-pop-in items-center gap-3 rounded-lg bg-black py-2 pr-2 pl-4 shadow-pop">
              {saveError ? (
                <ErrorText className="flex-1">{saveError}, so nothing was saved</ErrorText>
              ) : (
                <span className="flex-1 text-fg-muted">Unsaved changes</span>
              )}
              <Button
                variant="ghost"
                onClick={() => {
                  setRules(saved.rules.map(draft));
                  setErrors({});
                  setSaveError("");
                  setOpen(new Set());
                }}
              >
                Discard
              </Button>
              <Button variant="primary" loading={busy} onClick={save}>
                Save
              </Button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
