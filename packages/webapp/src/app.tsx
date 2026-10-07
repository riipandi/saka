/**
 * The loader mark the Go shell carries in #root — re-rendered here so the
 * handover from the static loader to the hydrated app reads as one mark
 * settling into place, not two different designs.
 */
function Mark() {
  return (
    <svg
      className="s-card__mark"
      aria-hidden="true"
      viewBox="0 0 36 12"
      xmlns="http://www.w3.org/2000/svg"
    >
      <circle cx="6" cy="6" r="4" />
      <circle cx="18" cy="6" r="4" opacity={0.7} />
      <circle cx="30" cy="6" r="4" opacity={0.45} />
    </svg>
  );
}

export default function App() {
  return (
    <main className="s-card">
      <Mark />
      <h1 className="s-card__title">Hello, Saka!</h1>
      <p className="s-card__text">
        Monolith Go, React, and TanStack application. The surfaces land here.
      </p>
      <p className="s-card__hint">
        served by Go · <code>v0.0.0</code>
      </p>
    </main>
  );
}
