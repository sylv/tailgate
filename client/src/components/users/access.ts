import type { User } from "../../api";

export const accessText = (u: User) =>
  u.allServices ? "Every service" : u.services.length ? u.services.join(", ") : "No services";
