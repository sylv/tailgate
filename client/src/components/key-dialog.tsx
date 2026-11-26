import type { FC } from "react";
import type { NewKey } from "../api";
import { Button } from "../ui/button";
import { CopyField } from "../ui/copy-field";
import { Dialog } from "../ui/dialog";
import { Labelled } from "../ui/input";

// a new key is shown once
export const KeyDialog: FC<{ k: NewKey | undefined; title: string; onClose: () => void }> = ({
  k,
  title,
  onClose,
}) => (
  <Dialog
    open={!!k}
    onOpenChange={(o) => !o && onClose()}
    title={title}
    description="Copy it now, it's only shown once"
    footer={
      <Button variant="primary" onClick={onClose}>
        Done
      </Button>
    }
  >
    {k && (
      <Labelled label="Key">
        <CopyField value={k.key} label="key" />
      </Labelled>
    )}
  </Dialog>
);
