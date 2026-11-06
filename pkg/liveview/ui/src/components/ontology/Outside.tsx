import type { Outside as OutsideT } from "@/lib/ontologyFinding";
import type { OntologyPalette } from "@/lib/ontologyPalette";
import { Runs } from "./Finding";

/**
 * The band outside the model: types the store's records carry that nothing
 * declares. Drawn below the diagram, dashed and in a warm grey no declared
 * thing wears, because that is what these are: not part of the model, and
 * never to be mistaken for something the schema said.
 */
export function Outside({ band, pal }: { band: OutsideT; pal: OntologyPalette }) {
  const s = pal.stray;
  return (
    <section
      aria-label="Outside the model"
      className="rounded-xl border border-dashed px-4 py-3"
      style={{ borderColor: s.border, background: `color-mix(in oklab, ${s.fill} 70%, var(--card))` }}
      data-outside
    >
      <h2 className="text-[11px] font-semibold uppercase tracking-wide" style={{ color: pal.draftInk }}>
        Outside the model
      </h2>
      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
        <Runs runs={band.runs} />
      </p>
      {band.groups.map((g) => (
        <div key={g.label} className="mt-3">
          <h3 className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{g.label}</h3>
          <ul className="mt-1.5 flex flex-wrap gap-1.5">
            {g.chips.map((c) => (
              <li
                key={c.name ?? ""}
                className="inline-flex max-w-full items-baseline gap-1.5 rounded-md border border-dashed px-2 py-0.5 text-xs"
                style={{ borderColor: s.border, color: s.ink, background: `color-mix(in oklab, ${s.fill} 85%, transparent)` }}
              >
                <span className={c.name ? "truncate" : "italic"} style={c.name ? undefined : { color: s.count }}>
                  {c.name ?? "(no type at all)"}
                </span>
                <span className="font-medium tabular-nums" style={{ color: s.count }}>
                  {c.count}
                </span>
              </li>
            ))}
            {g.more && (
              <li className="inline-flex items-baseline rounded-md border px-2 py-0.5 text-xs" style={{ borderColor: s.border, color: s.count }}>
                {g.more}
              </li>
            )}
          </ul>
        </div>
      ))}
    </section>
  );
}
