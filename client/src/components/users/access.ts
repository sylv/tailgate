import type { User } from "../../api";

export const accessText = (u: User) =>
  u.allApps ? "Every app" : u.apps.length ? u.apps.join(", ") : "No apps";
