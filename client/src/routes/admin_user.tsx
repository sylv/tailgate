import { ArrowLeftIcon, DotsThreeIcon, KeyIcon, TrashIcon } from "@phosphor-icons/react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, type Login, type NewKey, type User, type UserDetail } from "../api";
import { PageHeader } from "../components/admin-shell";
import { Confirm } from "../components/confirm";
import { ErrorText } from "../components/section";
import { KeyDialog } from "../components/key-dialog";
import { Keys } from "../components/users/keys";
import { Logins } from "../components/users/logins";
import { UserForm } from "../components/users/user-form";
import { POLL, useLoad } from "../hooks/use-load";
import { Button } from "../ui/button";
import { IconButton } from "../ui/icon-button";
import { Menu, MenuContent, MenuItem, MenuSeparator, MenuTrigger } from "../ui/menu";

const blank: User = { id: "", name: "", note: "", email: "", admin: false, allServices: false, services: [] };

const Back = () => (
  <Button variant="link" size="sm" icon={<ArrowLeftIcon />} asChild className="mb-3">
    <Link to="/admin/users">Users</Link>
  </Button>
);

export function UserRoute() {
  const { id } = useParams();
  const navigate = useNavigate();

  if (id === "new") {
    return (
      <>
        <Back />
        <PageHeader title="New user" />
        <div className="max-w-xl">
          <UserForm
            user={blank}
            saveLabel="Create user"
            onSave={async (u) => {
              const res = await api<{ id: string }>("POST", "/api/admin/users", u);
              navigate(`/admin/users/${res.id}`, { replace: true });
            }}
          />
        </div>
      </>
    );
  }
  return <ExistingUser id={id!} />;
}

function ExistingUser({ id }: { id: string }) {
  const navigate = useNavigate();
  const [users, error, reload] = useLoad(() => api<UserDetail[]>("GET", "/api/admin/users"), POLL);
  const [logins] = useLoad(() => api<Login[]>("GET", "/api/admin/logins"), POLL);
  const [confirming, setConfirming] = useState<"reset" | "delete">();
  const [revealed, setRevealed] = useState<{ k: NewKey; title: string }>();

  if (error) return <ErrorText>{error}</ErrorText>;
  if (!users) return null;
  const user = users.find((u) => u.id === id);
  if (!user) return <ErrorText>That user doesn't exist anymore</ErrorText>;
  const { id: uid, name, note, email, admin, allServices, services } = user;
  const profile: User = { id: uid, name, note, email, admin, allServices, services };

  return (
    <>
      <Back />
      <PageHeader
        title={user.name}
        description={[user.admin && "Admin", user.note].filter(Boolean).join(", ")}
        actions={
          <Menu>
            <MenuTrigger asChild>
              <IconButton label={`More for ${user.name}`} variant="secondary">
                <DotsThreeIcon weight="bold" />
              </IconButton>
            </MenuTrigger>
            <MenuContent align="end">
              <MenuItem
                icon={<KeyIcon />}
                onSelect={() => setConfirming("reset")}
                disabled={!user.keys.length}
              >
                Reset keys
              </MenuItem>
              <MenuSeparator />
              <MenuItem icon={<TrashIcon />} danger onSelect={() => setConfirming("delete")}>
                Delete user
              </MenuItem>
            </MenuContent>
          </Menu>
        }
      />
      <div className="grid gap-x-16 gap-y-12 lg:grid-cols-2">
        <UserForm
          // remount on save, so the saved user is the new baseline
          key={JSON.stringify(profile)}
          user={profile}
          saveLabel="Save"
          onSave={async (u) => {
            await api("PUT", "/api/admin/users/" + user.id, u);
            reload();
          }}
        />
        <div className="flex flex-col gap-10">
          <Keys
            user={user}
            reload={reload}
            onKey={(k) => setRevealed({ k, title: `New key for ${user.name}` })}
          />
          <Logins user={user} logins={logins ?? []} reload={reload} />
        </div>
      </div>
      <Confirm
        open={confirming === "reset"}
        onClose={() => setConfirming(undefined)}
        title={`Reset ${user.name}'s keys?`}
        description="Every key is revoked, their sessions end and one new key is made"
        action="Reset keys"
        onConfirm={async () => {
          const k = await api<NewKey>("POST", `/api/admin/users/${user.id}/reset-keys`);
          setRevealed({ k, title: `${user.name}'s new key` });
          reload();
        }}
      />
      <Confirm
        open={confirming === "delete"}
        onClose={() => setConfirming(undefined)}
        title={`Delete ${user.name}?`}
        description="All of their keys and sessions end"
        action="Delete user"
        onConfirm={async () => {
          await api("DELETE", "/api/admin/users/" + user.id);
          navigate("/admin/users");
        }}
      />
      <KeyDialog k={revealed?.k} title={revealed?.title ?? ""} onClose={() => setRevealed(undefined)} />
    </>
  );
}
