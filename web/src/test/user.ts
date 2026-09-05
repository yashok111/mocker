import userEvent from "@testing-library/user-event";

// fill() puts a whole string into a field with ONE input event, where
// userEvent.type() sends one per character and React re-renders the screen
// after each. On the big editors that is the whole cost of a test: typing
// 25 characters into the operation editor's preview form took 1.4 s, a
// 201-rune label into the checkpoint form 0.6 s, 65 emoji into the login
// form 0.7 s (measured 2026-09-05, vitest 5 on happy-dom). Use it for text
// the test only needs IN the field; keep userEvent.type() where the test is
// about what happens per keystroke (a search filter, a validator that runs
// as you type).
export async function fill(element: HTMLElement, text: string): Promise<void> {
  await userEvent.click(element);
  await userEvent.paste(text);
}
