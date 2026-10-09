// The shared Select (components/ui/select-field) from a test: its value
// is on the trigger (data-value); choosing opens the list and presses
// the option the way a pointer does (Base UI commits a pointer-started
// click), then waits for the trigger to say so.
import { act, fireEvent, waitFor } from "@testing-library/react"

/** The option elements of the trigger's open list (the one its
 * aria-controls names: another select's closing list is not it). */
function listed(trigger: HTMLElement): HTMLElement[] {
  const id = trigger.getAttribute("aria-controls")
  const list = id ? document.getElementById(id) : null
  return list ? Array.from(list.querySelectorAll<HTMLElement>('[role="option"]')) : []
}

/** The trigger's value: what a native select's .value was. */
export function valueOf(trigger: HTMLElement): string {
  return trigger.getAttribute("data-value") ?? ""
}

async function open(trigger: HTMLElement) {
  if (trigger.getAttribute("aria-expanded") !== "true") fireEvent.click(trigger)
  await waitFor(() => {
    if (trigger.getAttribute("aria-expanded") !== "true" || listed(trigger).length === 0)
      throw new Error("the list did not open")
  })
}

async function close(trigger: HTMLElement) {
  if (trigger.getAttribute("aria-expanded") !== "true") return
  const at = document.activeElement ?? trigger
  fireEvent.keyDown(at, { key: "Escape" })
  await waitFor(() => {
    if (trigger.getAttribute("aria-expanded") === "true") throw new Error("the list did not close")
  })
}

/** choose picks the option whose value is `value`. */
export async function choose(trigger: HTMLElement, value: string) {
  await open(trigger)
  const opt = listed(trigger).find((o) => o.getAttribute("data-value") === value)
  if (!opt) {
    await close(trigger)
    throw new Error(`no option ${JSON.stringify(value)} in ${trigger.getAttribute("aria-label")}`)
  }
  if (opt.getAttribute("aria-disabled") === "true" || opt.hasAttribute("data-disabled")) {
    await close(trigger)
    throw new Error(`option ${JSON.stringify(value)} of ${trigger.getAttribute("aria-label")} is disabled`)
  }
  await act(async () => {
    fireEvent.pointerDown(opt, { pointerType: "mouse" })
    fireEvent.click(opt)
  })
  await waitFor(() => {
    if (valueOf(trigger) !== value)
      throw new Error(`${trigger.getAttribute("aria-label")} is ${valueOf(trigger)}, not ${value}`)
  })
  await close(trigger)
}

export type ListedOption = { value: string; text: string; disabled: boolean; title: string; el: HTMLElement }

/** optionsOf opens the list, reads its options and closes it again. */
export async function optionsOf(trigger: HTMLElement): Promise<ListedOption[]> {
  await open(trigger)
  const out = listed(trigger).map((el) => ({
    value: el.getAttribute("data-value") ?? "",
    text: el.textContent,
    disabled: el.hasAttribute("data-disabled") || el.getAttribute("aria-disabled") === "true",
    title: el.getAttribute("title") ?? "",
    el,
  }))
  await close(trigger)
  return out
}
