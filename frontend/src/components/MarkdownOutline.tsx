import {
  Accessor,
  For,
  Show,
  createEffect,
  createSignal,
  onCleanup,
} from "solid-js"
import "./markdown-outline.css"

export function MarkdownOutline(props: {
  article: Accessor<HTMLElement | undefined>
  revision: string
  label: string
  emptyLabel: string
}) {
  const [headings, setHeadings] = createSignal<HTMLElement[]>([])
  createEffect(() => {
    const article = props.article()
    void props.revision
    let cancelled = false
    queueMicrotask(() => {
      if (cancelled) return
      setHeadings(
        Array.from(
          article?.querySelectorAll<HTMLElement>("h1,h2,h3,h4,h5,h6") ?? [],
        ).filter((heading) => Boolean(heading.textContent?.trim())),
      )
    })
    onCleanup(() => {
      cancelled = true
    })
  })
  const firstLevel = () =>
    Math.min(...headings().map((heading) => Number(heading.tagName.slice(1))))
  const jump = (heading: HTMLElement) => {
    const nav =
      document.querySelector(".nav")?.getBoundingClientRect().bottom ?? 0
    const top = heading.getBoundingClientRect().top - Math.max(0, nav) - 12
    window.scrollBy({
      top,
      behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
        ? "auto"
        : "smooth",
    })
  }

  return (
    <details class="markdown-outline" open>
      <summary>{props.label}</summary>
      <Show when={headings().length} fallback={<p>{props.emptyLabel}</p>}>
        <nav aria-label={props.label}>
          <ul>
            <For each={headings()}>
              {(heading) => (
                <li
                  style={{
                    "padding-inline-start": `${(Number(heading.tagName.slice(1)) - firstLevel()) * 0.75}rem`,
                  }}
                >
                  <button type="button" onClick={() => jump(heading)}>
                    {heading.textContent?.trim()}
                  </button>
                </li>
              )}
            </For>
          </ul>
        </nav>
      </Show>
    </details>
  )
}
