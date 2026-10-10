// jsdom lays nothing out: the virtualised lists' tests (plan H2) give
// a virtualised scroller a browser's scroll geometry.
import { act } from "@testing-library/react"

/** jsdom lays nothing out and has no Element.scrollTo: the
 * virtualisers read a viewport-tall window and estimated rows
 * (lib/virtual.ts), a scroll is a scrollTop write plus its event, and a
 * virtualised scroller is as tall as its spacer says (a browser's
 * scrollHeight) and shows a viewport of it (its clientHeight). */
export function stubScroll(): () => void {
  const proto = HTMLElement.prototype
  const saved = {
    scrollTo: Element.prototype.scrollTo,
    scrollHeight: Object.getOwnPropertyDescriptor(
      Element.prototype,
      "scrollHeight"
    ),
    clientHeight: Object.getOwnPropertyDescriptor(
      Element.prototype,
      "clientHeight"
    ),
  }
  Element.prototype.scrollTo = function (
    this: Element,
    opts?: ScrollToOptions | number
  ) {
    const top = typeof opts === "number" ? opts : opts?.top
    // As a browser: a scroll that moves nothing fires no event.
    if (typeof top !== "number" || top === this.scrollTop) return
    this.scrollTop = top
    this.dispatchEvent(new Event("scroll"))
  }
  const virtualised = (el: HTMLElement) => el.hasAttribute("data-virtual")
  Object.defineProperty(proto, "scrollHeight", {
    configurable: true,
    get(this: HTMLElement) {
      if (!virtualised(this)) return 0
      const spacer = this.querySelector<HTMLElement>(":scope > .relative")
      const h = spacer ? Number.parseFloat(spacer.style.height) : 0
      return Number.isFinite(h) ? h : 0
    },
  })
  Object.defineProperty(proto, "clientHeight", {
    configurable: true,
    get(this: HTMLElement) {
      return virtualised(this) ? window.innerHeight : 0
    },
  })
  return () => {
    Element.prototype.scrollTo = saved.scrollTo
    delete (proto as { scrollHeight?: number }).scrollHeight
    delete (proto as { clientHeight?: number }).clientHeight
  }
}

export function scrollTo(el: Element, top: number) {
  act(() => {
    el.scrollTop = top
    el.dispatchEvent(new Event("scroll"))
  })
}
