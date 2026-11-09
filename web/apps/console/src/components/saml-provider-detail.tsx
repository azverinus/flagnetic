import type { AuthProvider, IdPCertificate, SpCertificate } from "@flagnetic/api";
import { Trans, useLingui } from "@lingui/react/macro";
import { useMutation } from "@tanstack/react-query";
import { DownloadIcon, KeyRoundIcon, RotateCwIcon } from "lucide-react";
import { type ReactNode, useState } from "react";
import { toast } from "sonner";

import { CopyableValue } from "@/components/copyable-value";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { formatDateTime } from "@/i18n/format";
import { authProviderApi } from "@/lib/auth-providers";
import { ApiError } from "@/lib/errors";
import { pastedMetadata, spMetadataPath } from "@/lib/saml-providers";

/**
 * What to give the identity provider so it knows the instance: the ACS URL
 * it posts responses to, the entity ID it addresses them to, and the SP
 * metadata that holds both with the instance's certificates. The instance
 * serves that metadata only while the provider is enabled.
 */
export function SamlRegistration({ provider }: { provider: AuthProvider }) {
  const { t } = useLingui();
  const { acs_url: acs, entity_id: entity, metadata_url: metadata } = provider;
  return (
    <section aria-labelledby="register" className="flex flex-col gap-3">
      <h2 id="register" className="text-base font-semibold">
        <Trans>Register the instance at the identity provider</Trans>
      </h2>
      {acs && entity && metadata ? (
        <>
          <p className="text-sm text-muted-foreground">
            <Trans>
              Import the SP metadata at the identity provider, or enter the ACS URL and the entity
              ID by hand.
            </Trans>
          </p>
          <CopyableValue label={t`ACS URL`} value={acs} />
          <CopyableValue label={t`Entity ID`} value={entity} />
          <CopyableValue label={t`SP metadata URL`} value={metadata} />
          {provider.enabled ? (
            <div>
              <Button variant="outline" asChild>
                <a
                  href={spMetadataPath(provider.slug)}
                  download={`${provider.slug}-sp-metadata.xml`}
                >
                  <DownloadIcon aria-hidden />
                  <Trans>Download SP metadata</Trans>
                </a>
              </Button>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">
              <Trans>
                The instance serves the SP metadata only while the provider is enabled. Enable it to
                download the metadata or let the identity provider fetch it.
              </Trans>
            </p>
          )}
        </>
      ) : (
        <p className="text-sm text-muted-foreground">
          <Trans>
            The ACS URL and the entity ID are built from the instance's public address
            (FLAGNETIC_BASE_URL), which is not set. Set it, and the addresses show here.
          </Trans>
        </p>
      )}
    </section>
  );
}

/**
 * What the identity provider's metadata said when the instance last read
 * it, and whether it is read again: metadata fetched from a URL is
 * refreshed daily, pasted metadata never.
 */
export function SamlIdentityProvider({ provider }: { provider: AuthProvider }) {
  const { i18n } = useLingui();
  const pasted = pastedMetadata(provider);
  const read = provider.idp_metadata_refreshed_at;
  return (
    <section aria-labelledby="identity-provider" className="flex flex-col gap-3">
      <h2 id="identity-provider" className="text-base font-semibold">
        <Trans>Identity provider</Trans>
      </h2>
      <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-[max-content_1fr]">
        <Row term={<Trans>Metadata</Trans>}>
          {pasted ? (
            <Trans>Pasted</Trans>
          ) : (
            <code className="font-mono text-xs break-all">{provider.idp_metadata_url}</code>
          )}
        </Row>
        <Row term={<Trans>Last read</Trans>}>
          {read ? formatDateTime(i18n, read) : null}{" "}
          <span className="text-muted-foreground">
            {pasted ? (
              <Trans>Pasted metadata is never read again; paste a new document to change it.</Trans>
            ) : (
              <Trans>Metadata from a URL is read again every day.</Trans>
            )}
          </span>
        </Row>
        <Row term={<Trans>Entity ID</Trans>}>
          <Code value={provider.idp_entity_id} />
        </Row>
        <Row term={<Trans>Sign-on URL</Trans>}>
          <Code value={provider.idp_sso_url} />
        </Row>
        <Row term={<Trans>Signing certificates</Trans>}>
          <IdPCertificates certificates={provider.idp_certificates ?? []} />
        </Row>
      </dl>
    </section>
  );
}

/** The certificates an identity provider signs with, by fingerprint and expiry. */
export function IdPCertificates({ certificates }: { certificates: IdPCertificate[] }) {
  const { i18n } = useLingui();
  return (
    <ul className="flex flex-col gap-2">
      {certificates.map((certificate) => {
        const expires = formatDateTime(i18n, certificate.not_after);
        return (
          <li key={certificate.sha256} className="flex flex-col gap-0.5">
            <code className="font-mono text-xs break-all">{certificate.sha256}</code>
            <span className="text-xs text-muted-foreground">
              <Trans>SHA-256 fingerprint, expires {expires}</Trans>
            </span>
          </li>
        );
      })}
    </ul>
  );
}

/**
 * The instance's certificates for the provider, and their rotation in two
 * steps: start puts the next key pair beside the current one, so the SP
 * metadata lists both; once the identity provider has imported that
 * metadata, complete makes the next one current and destroys the old key.
 */
export function SamlCertificates({
  provider,
  onChanged,
  onReload,
}: {
  provider: AuthProvider;
  /** The provider a rotation step answered. */
  onChanged: (saved: AuthProvider) => void;
  /** Reads the provider again, after a refusal that says someone else changed it. */
  onReload: () => void;
}) {
  const { t } = useLingui();
  const [step, setStep] = useState<"start" | "complete">();
  const name = provider.display_name;
  const pending = provider.rotation_pending === true;
  const close = () => {
    rotation.reset();
    setStep(undefined);
  };
  const rotation = useMutation({
    mutationFn: (which: "start" | "complete") =>
      which === "start"
        ? authProviderApi.rotate(provider.slug)
        : authProviderApi.completeRotation(provider.slug),
    meta: { toast: false },
    onSuccess: (saved, which) => {
      onChanged(saved);
      setStep(undefined);
      toast.success(
        which === "start"
          ? t`The next certificate of ${name} is generated.`
          : t`The rotation of ${name} is complete.`,
      );
    },
    onError: (error) => {
      // Someone else started or completed it: show the provider as it is.
      if (
        error instanceof ApiError &&
        (error.code === "rotation_pending" || error.code === "no_rotation_pending")
      ) {
        onReload();
      }
    },
  });

  return (
    <section aria-labelledby="certificates" className="flex flex-col gap-3">
      <h2 id="certificates" className="text-base font-semibold">
        <Trans>Certificates</Trans>
      </h2>
      <p className="text-sm text-muted-foreground">
        <Trans>
          The instance signs its requests with the current certificate's key and decrypts assertions
          encrypted to any certificate listed here. The SP metadata lists them all.
        </Trans>
      </p>
      <ul aria-label={t`SP certificates`} className="flex flex-col gap-2">
        {(provider.sp_certificates ?? []).map((certificate) => (
          <SpCertificateItem key={certificate.sha256} certificate={certificate} />
        ))}
      </ul>
      {pending ? (
        <p role="status" className="rounded-md border bg-muted p-3 text-sm">
          <Trans>
            A rotation is pending. Import the SP metadata at the identity provider again, so that it
            knows the next certificate, then complete the rotation. Until then nothing changes for
            the identity provider.
          </Trans>
        </p>
      ) : null}
      <div>
        {pending ? (
          <Button variant="outline" onClick={() => setStep("complete")}>
            <KeyRoundIcon aria-hidden />
            <Trans>Complete rotation</Trans>
          </Button>
        ) : (
          <Button variant="outline" onClick={() => setStep("start")}>
            <RotateCwIcon aria-hidden />
            <Trans>Start certificate rotation</Trans>
          </Button>
        )}
      </div>
      <ConfirmDialog
        open={step === "start"}
        onOpenChange={(open) => (open ? undefined : close())}
        title={t`Start certificate rotation`}
        confirmLabel={<Trans>Generate next certificate</Trans>}
        pending={rotation.isPending}
        refusal={rotation.error}
        onConfirm={() => rotation.mutate("start")}
      >
        <p>
          <Trans>
            A new key pair is generated beside the current one, valid for three years. The SP
            metadata lists both certificates, and assertions encrypted to either are accepted, so
            sign-ins keep working while the identity provider imports the new metadata.
          </Trans>
        </p>
      </ConfirmDialog>
      <ConfirmDialog
        open={step === "complete"}
        onOpenChange={(open) => (open ? undefined : close())}
        title={t`Complete certificate rotation`}
        confirmLabel={<Trans>Complete rotation</Trans>}
        destructive
        pending={rotation.isPending}
        refusal={rotation.error}
        onConfirm={() => rotation.mutate("complete")}
      >
        <p>
          <Trans>
            The next certificate becomes current and the old key is destroyed. An identity provider
            that still encrypts assertions to the old certificate can no longer sign anyone in, so
            complete the rotation only once it has imported the new SP metadata. This cannot be
            undone.
          </Trans>
        </p>
      </ConfirmDialog>
    </section>
  );
}

function SpCertificateItem({ certificate }: { certificate: SpCertificate }) {
  const { i18n } = useLingui();
  const expires = formatDateTime(i18n, certificate.not_after);
  return (
    <li className="flex flex-col gap-1 rounded-md border p-3">
      <span>
        {certificate.status === "current" ? (
          <Badge>
            <Trans>Current</Trans>
          </Badge>
        ) : (
          <Badge variant="muted">
            <Trans>Next</Trans>
          </Badge>
        )}
      </span>
      <code className="font-mono text-xs break-all">{certificate.sha256}</code>
      <span className="text-xs text-muted-foreground">
        <Trans>SHA-256 fingerprint, expires {expires}</Trans>
      </span>
    </li>
  );
}

function Row({ term, children }: { term: ReactNode; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{term}</dt>
      <dd>{children}</dd>
    </>
  );
}

function Code({ value }: { value: string | undefined }) {
  return <code className="font-mono text-xs break-all">{value}</code>;
}
