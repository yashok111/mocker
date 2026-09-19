import { useState, type ReactElement } from "react";
import { Alert } from "@mantine/core";
import { type ApiDocument, type DocumentSelection } from "./documentModel";
import { DocumentMetadataForm } from "./forms/DocumentMetadataForm";
import { OperationForm } from "./forms/OperationForm";
import { SharedSchemaForm } from "./forms/SharedSchemaForm";
import { FormDraftProvider } from "./forms/FormDraftContext";
import { createFormDraftStore, type FormDraftStore } from "./forms/formDraftStore";
export { createFormDraftStore, type FormDraftStore } from "./forms/formDraftStore";

export type { ApiDocument, DocumentSelection } from "./documentModel";

export interface DocumentFormProps {
  document: ApiDocument;
  selection: DocumentSelection;
  onChange: (document: ApiDocument) => void;
  draftStore?: FormDraftStore;
}

export function DocumentForm({ draftStore, ...props }: DocumentFormProps): ReactElement {
  const [fallback] = useState(createFormDraftStore);
  return (
    <FormDraftProvider store={draftStore ?? fallback}>
      <SelectedDocumentForm {...props} />
    </FormDraftProvider>
  );
}

function SelectedDocumentForm({ document, selection, onChange }: DocumentFormProps): ReactElement {
  if (selection.kind === "document") {
    return <DocumentMetadataForm document={document} onChange={onChange} />;
  }
  if (selection.kind === "operation") {
    return (
      <OperationForm
        key={`${selection.path}:${selection.method}`}
        document={document}
        location={{ path: selection.path, method: selection.method }}
        onChange={onChange}
      />
    );
  }
  if (selection.kind === "schema") {
    return (
      <SharedSchemaForm
        key={selection.name}
        document={document}
        name={selection.name}
        onChange={onChange}
      />
    );
  }
  return <Alert color="yellow">Выберите документ, операцию или схему.</Alert>;
}
