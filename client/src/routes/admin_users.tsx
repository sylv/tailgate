import { CaretRightIcon, PlusIcon } from "@phosphor-icons/react";
import { Link } from "react-router";
import { api, type UserDetail } from "../api";
import { PageHeader } from "../components/admin-shell";
import { Empty, ErrorText, List } from "../components/section";
import { accessText } from "../components/users/access";
import { POLL, useLoad } from "../hooks/use-load";
import { ago } from "../lib/time";
import { Button } from "../ui/button";

export function UsersRoute() {
  const [users, error] = useLoad(() => api<UserDetail[]>("GET", "/api/admin/users"), POLL);
  return (
    <>
      <PageHeader
        title="Users"
        actions={
          <Button variant="primary" icon={<PlusIcon weight="bold" />} asChild>
            <Link to="/admin/users/new">Add user</Link>
          </Button>
        }
      />
      <ErrorText>{error}</ErrorText>
      {users &&
        (users.length === 0 ? (
          <Empty>No users yet</Empty>
        ) : (
          <List>
            {users.map((u) => (
              <UserRow key={u.id} user={u} />
            ))}
          </List>
        ))}
    </>
  );
}

function UserRow({ user }: { user: UserDetail }) {
  const used = Math.max(0, ...user.keys.map((k) => k.lastUsedAt ?? 0));
  const signIn = [
    user.keys.length > 0 && `${user.keys.length} ${user.keys.length === 1 ? "key" : "keys"}`,
    ...user.logins.map((l) => l.name),
  ].filter(Boolean);
  return (
    <li>
      <Link
        to={`/admin/users/${user.id}`}
        className="group grid grid-cols-[2rem_minmax(0,1fr)_1rem] items-center gap-x-4 px-4 py-3 hover:bg-raised md:grid-cols-[2rem_minmax(0,1fr)_minmax(0,14rem)_9rem_1rem]"
      >
        <span className="flex size-8 items-center justify-center rounded-full border border-line text-[13px] font-medium text-fg-muted uppercase">
          {user.name[0]}
        </span>
        <span className="min-w-0">
          <span className="flex items-baseline gap-2">
            <span className="truncate underline-offset-2 group-hover:underline">{user.name}</span>
            <span className="truncate text-[13px] text-fg-faint">
              {[user.admin && "admin", user.note].filter(Boolean).join(", ")}
            </span>
          </span>
          <span className="block truncate text-[13px] text-fg-muted">{accessText(user)}</span>
        </span>
        <span className="hidden truncate text-[13px] text-fg-muted md:block">
          {signIn.length ? signIn.join(", ") : <span className="text-warning">No way to sign in</span>}
        </span>
        <span className="hidden text-right text-[13px] text-fg-muted tabular-nums md:block">
          {used ? `Used ${ago(used)}` : user.keys.length ? "Never used" : ""}
        </span>
        <CaretRightIcon className="size-4 text-fg-faint group-hover:text-fg" />
      </Link>
    </li>
  );
}
