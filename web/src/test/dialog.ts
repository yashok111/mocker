import { within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

// cancelDialog dismisses a confirmation dialog by its cancel button.
//
// Seven tests across four files reached for `within(dialog).getByText("Отмена")`
// — the Russian label of Mantine's `modals.openConfirmModal`, which is product
// COPY: rewording it to «Не сейчас» on one screen would break tests on four
// unrelated ones, and the failure would read as a broken screen rather than as
// a renamed button. The button carries `data-testid="dialog-cancel"` instead,
// added at every confirm-modal call site (and on RollbackModalBody, the one
// hand-built dialog body those tests dismiss).
//
// The scope is deliberately narrow: only DIALOGS carry the id. An inline form's
// own «Отмена» button keeps its screen-specific testid, because a page can show
// a form and a dialog at once and two `dialog-cancel` nodes would be ambiguous.
export async function cancelDialog(dialog: HTMLElement): Promise<void> {
  await userEvent.click(within(dialog).getByTestId("dialog-cancel"));
}
