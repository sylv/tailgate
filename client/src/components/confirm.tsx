import { useState, type FC, type ReactNode } from "react";
import { Button } from "../ui/button";
import { Dialog } from "../ui/dialog";
import { ErrorText } from "./section";

// asks before something that can't be undone, and stays open with the error if it fails
export const Confirm: FC<{
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description: ReactNode;
  action: string;
  onConfirm: () => Promise<unknown>;
}> = ({ open, onClose, title, description, action, onConfirm }) => {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => !o && onClose()}
      size="sm"
      title={title}
      description={description}
      footer={
        <>
          <ErrorText className="mr-auto self-center">{error}</ErrorText>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant="danger"
            loading={busy}
            onClick={async () => {
              setBusy(true);
              setError("");
              try {
                await onConfirm();
                onClose();
              } catch (e) {
                setError((e as Error).message);
              }
              setBusy(false);
            }}
          >
            {action}
          </Button>
        </>
      }
    />
  );
};
