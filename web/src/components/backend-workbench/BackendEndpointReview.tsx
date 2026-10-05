import { useState } from "react";
import { Button, Checkbox, Stack, Text, TextInput, Title } from "@mantine/core";
import type { BackendChangeProposalDetail } from "@/api/generated/schemas";
import type { B43Start } from "./BackendChangePackage";
import classes from "./BackendAnalysisControls.module.css";
export function BackendEndpointReview({
  sourceRevisionId,
  proposal,
  disabled,
  intentDisabled,
  onStart,
}: {
  sourceRevisionId: string;
  proposal?: BackendChangeProposalDetail;
  disabled: boolean;
  intentDisabled: boolean;
  onStart: B43Start;
}) {
  const [before, setBefore] = useState(sourceRevisionId),
    [after, setAfter] = useState("");
  const [beforeId, setBeforeId] = useState(""),
    [afterId, setAfterId] = useState("");
  const [removed, setRemoved] = useState(false),
    [intent, setIntent] = useState(false);
  const uuid = /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/;
  const valid = [before, after, beforeId, ...(removed ? [] : [afterId])].every((v) => uuid.test(v));
  return (
    <section aria-label="Обзор endpoint">
      <Stack gap="sm">
        <Title order={4}>Обзор endpoint</Title>
        <Text size="sm">
          Выберите точные endpoint UUID по обеим сторонам. Удаление сохраняет пути и основания
          стороны «до». Выполнение и доставка событий не проверяются.
        </Text>
        <fieldset disabled={disabled} style={{ border: 0, padding: 0, minWidth: 0 }}>
          <Stack gap="sm">
            <div className={classes.grid}>
              <TextInput
                label="Ревизия endpoint до"
                value={before}
                onChange={(e) => setBefore(e.currentTarget.value)}
              />
              <TextInput
                label="UUID endpoint до"
                value={beforeId}
                onChange={(e) => setBeforeId(e.currentTarget.value)}
              />
            </div>
            <div className={classes.grid}>
              <TextInput
                label="Ревизия endpoint после"
                value={after}
                onChange={(e) => setAfter(e.currentTarget.value)}
              />
              <TextInput
                label="UUID endpoint после"
                value={afterId}
                disabled={removed}
                onChange={(e) => setAfterId(e.currentTarget.value)}
              />
            </div>
            <Checkbox
              label="Endpoint удалён — после отсутствует (null)"
              checked={removed}
              onChange={(e) => setRemoved(e.currentTarget.checked)}
            />
            {proposal && (
              <Checkbox
                label="Сравнить с сохранённым намерением предложения"
                checked={intent}
                disabled={intentDisabled}
                onChange={(e) => setIntent(e.currentTarget.checked)}
              />
            )}
            <Button
              disabled={!valid || disabled || (intent && intentDisabled)}
              onClick={() =>
                void onStart({
                  kind: "endpoint_review",
                  fromRevisionId: before,
                  toRevisionId: after,
                  beforeEndpointId: beforeId,
                  afterEndpointId: removed ? null : afterId,
                  ...(intent && proposal
                    ? {
                        changeProposal: {
                          proposalId: proposal.proposal.id,
                          proposalRevisionId: proposal.revision.id,
                        },
                      }
                    : {}),
                  limits: {},
                  observationMode: "none",
                  idempotencyKey: crypto.randomUUID(),
                })
              }
            >
              Обзор endpoint
            </Button>
          </Stack>
        </fieldset>
      </Stack>
    </section>
  );
}
