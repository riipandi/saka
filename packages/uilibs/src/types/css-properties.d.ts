import type { CSSProperties as ReactCSSProperties } from 'react'

declare module 'react' {
  /**
   * React DOM passes unknown style keys straight through, and custom
   * properties (`--*`) are how dynamic values — measured widths, indents,
   * virtualizer offsets — reach the stylesheet without inline re-renders.
   * csstype's `Properties` interface predates that usage and carries no
   * custom-property index, so this records the runtime truth.
   */
  interface CSSProperties extends ReactCSSProperties {
    [customProperty: `--${string}`]: string | number | undefined
  }
}
