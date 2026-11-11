"use client";

import {Field, Input, Modal} from "@aoctech/ui";
import {useMutation} from "@tanstack/react-query";
import {useState} from "react";
import {useTranslation} from "react-i18next";
import {toast} from "sonner";

import {messageFor} from "@/lib/api/client";
import {setProductDetails} from "@/lib/api/console";
import {useConsoleScope} from "@/lib/console/scope";
import limits from "@/lib/limits.json";
import {useFieldErrors} from "@/lib/useFieldErrors";

export function ProductDetailsDialog({
  open,
  onClose,
  productId,
  description,
  url,
  onSaved,
}: {
  open: boolean
  onClose: () => void
  productId: string
  description: string
  url: string
  onSaved: () => void
}) {
  const {t} = useTranslation();
  const scope = useConsoleScope();
  const [desc, setDesc] = useState(description);
  const [address, setAddress] = useState(url);
  const fe = useFieldErrors(["description", "url"], {keepOthers: false});
  const save = useMutation({
    mutationFn: () => setProductDetails(scope, productId, {description: desc.trim(), url: address.trim()}),
    onSuccess: () => {
      toast.success(t("console.product.detailsSaved"));
      onSaved();
      onClose();
    },
    onError: error => { if (!fe.set(error)) toast.error(messageFor(error)); },
  });

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("console.product.editDetails")}
      cancelLabel={t("common.cancel")}
      submitLabel={t("console.product.save")}
      loading={save.isPending}
      onSubmit={() => { fe.reset(); save.mutate(); }}
    >
      <div className="space-y-4">
        <Field label={t("console.catalog.descriptionLabel")} htmlFor="details-description" error={fe.of("description")}>
          <Input
            id="details-description"
            maxLength={limits.text.productDescription}
            value={desc}
            {...fe.props("description", "details-description")}
            onChange={event => { setDesc(event.target.value); fe.clear("description"); }}
          />
        </Field>
        <Field
          label={t("console.catalog.urlLabel")}
          htmlFor="details-url"
          hint={t("console.catalog.urlHint")}
          error={fe.of("url")}
        >
          <Input
            id="details-url"
            type="url"
            maxLength={limits.text.productURL}
            value={address}
            {...fe.props("url", "details-url")}
            onChange={event => { setAddress(event.target.value); fe.clear("url"); }}
          />
        </Field>
        {fe.general && <p role="alert" className="text-sm text-danger">{fe.general}</p>}
      </div>
    </Modal>
  );
}
