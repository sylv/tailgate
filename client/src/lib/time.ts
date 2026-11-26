const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 31536000],
  ["month", 2592000],
  ["week", 604800],
  ["day", 86400],
  ["hour", 3600],
  ["minute", 60],
];

const fmt = new Intl.RelativeTimeFormat("en", { numeric: "auto" });

/** "3 days ago" from unix seconds, "just now" under a minute */
export const ago = (seconds: number) => {
  const d = Date.now() / 1000 - seconds;
  for (const [unit, size] of units) if (d >= size) return fmt.format(-Math.floor(d / size), unit);
  return "just now";
};
