// The ontology drawn as lanes: the SVG for a layout from lib/ontologyLayout.
//
// Lane bands first, then links, then boxes, then the links' names: the curves
// pass behind the labels they would otherwise cross out, and a name is drawn
// over the boxes because a curve between two boxes one row apart sometimes
// cannot be bowed clear of everything, and a name half-covered by a box reads
// as a different name ("bookedBy" arriving as "y") rather than as an overlap.

import { useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import type { OntologyAnswer } from "@/api/ontology";
import { cardinalityWord, key, layout, litBy, type Box, type Lane, type LinkRoute, type Selection } from "@/lib/ontologyLayout";
import { laneColor, type OntologyPalette } from "@/lib/ontologyPalette";
import { cn } from "@/lib/utils";

interface Props {
  rep: OntologyAnswer;
  pal: OntologyPalette;
  sel: Selection | null;
  onSelect: (sel: Selection | null) => void;
}

/** Enter and Space press a focused shape, the way they press a button. */
function pressable(label: string, onPress: () => void) {
  return {
    role: "button",
    tabIndex: 0,
    "aria-label": label,
    onClick: (e: { stopPropagation: () => void }) => {
      e.stopPropagation();
      onPress();
    },
    onKeyDown: (e: KeyboardEvent) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        onPress();
      }
    },
  };
}

function LaneShape({ lane, pal, compact, onSelect, chosen }: { lane: Lane; pal: OntologyPalette; compact: boolean; onSelect: Props["onSelect"]; chosen: boolean }) {
  const color = laneColor(pal, lane.colorIndex);
  const { header: h } = lane;
  const iface = lane.iface;
  const head = (
    <>
      <rect x={h.x} y={h.y} width={h.w} height={h.h} rx={8} className="fill-card" stroke={color} strokeOpacity={iface ? 0.7 : 0.4} strokeWidth={chosen ? 2 : 1} strokeDasharray={iface ? undefined : "4 3"} />
      {compact ? (
        <>
          <text x={h.x + 11} y={h.y + 17} fontSize={12} fontWeight={600} fill={iface ? color : undefined} className={cn(!iface && "fill-muted-foreground italic")}>
            {lane.title}
          </text>
          <text x={h.x + 11} y={h.y + 32} fontSize={10} className="fill-muted-foreground">
            {iface ? `${lane.subtitle} · ${lane.footnote}` : lane.subtitle}
          </text>
        </>
      ) : iface ? (
        <>
          <text x={h.x + 11} y={h.y + 20} fontSize={12} fontWeight={600} fill={color}>
            {lane.title}
          </text>
          <text x={h.x + 11} y={h.y + 35} fontSize={10} className="fill-muted-foreground">
            {lane.subtitle}
          </text>
          <text x={h.x + 11} y={h.y + 47} fontSize={10} className="fill-muted-foreground/80">
            {lane.footnote}
          </text>
        </>
      ) : (
        <>
          <text x={h.x + 11} y={h.y + 26} fontSize={11} fontStyle="italic" className="fill-muted-foreground">
            {lane.title}
          </text>
          <text x={h.x + 11} y={h.y + 41} fontSize={10} className="fill-muted-foreground/80">
            {lane.subtitle}
          </text>
        </>
      )}
    </>
  );
  return (
    <>
      <rect x={lane.band.x} y={lane.band.y} width={lane.band.w} height={lane.band.h} rx={10} className={cn("stroke-border", iface ? "fill-muted/45" : "fill-muted/20")} />
      {iface ? (
        <g className="cursor-pointer" data-iface={iface.api_name} {...pressable(`Interface ${iface.api_name}: light its implementors`, () => onSelect({ kind: "interface", name: iface.api_name }))}>
          <title>{`${iface.api_name}: ${lane.footnote}`}</title>
          {head}
        </g>
      ) : (
        <g>{head}</g>
      )}
    </>
  );
}

function LinkCurve({ r, pal, chosen, onSelect }: { r: LinkRoute; pal: OntologyPalette; chosen: boolean; onSelect: Props["onSelect"] }) {
  const stroke = r.live ? pal.link : pal.linkIdle;
  return (
    <g className="cursor-pointer" data-link={r.lt.api_name} {...pressable(`Link type ${r.lt.api_name}`, () => onSelect({ kind: "link", name: r.lt.api_name }))}>
      <title>{`${r.lt.api_name}: ${r.lt.multiplicity || "multiplicity not declared on both sides"}`}</title>
      {/* A wide invisible stroke, so a thin curve can be hit with a finger. */}
      <path d={r.d} fill="none" stroke="transparent" strokeWidth={14} />
      <path d={r.d} fill="none" stroke={stroke} strokeWidth={chosen ? 2.6 : r.live ? 1.6 : 1.2} strokeDasharray={r.live ? undefined : "5 4"} strokeOpacity={0.9} />
      {r.ends.map((e) => (
        <g key={e.side}>
          <title>{`${e.end.api_name || `side ${e.side.toUpperCase()}`}: reaches ${cardinalityWord(e.end.cardinality)} ${e.end.object_type}${e.fk ? `; carries the foreign key ${e.end.foreign_key}` : ""}`}</title>
          <circle cx={e.x} cy={e.y} r={8} stroke={stroke} fill={e.fk ? pal.fk : undefined} className={cn(!e.fk && "fill-card")} data-fk={e.fk ? "1" : undefined} />
          <text x={e.x} y={e.y + 4} fontSize={11} textAnchor="middle" fill={e.fk ? pal.fkInk : undefined} className={cn(!e.fk && "fill-muted-foreground")}>
            {e.glyph}
          </text>
        </g>
      ))}
    </g>
  );
}

function LinkLabel({ r, pal, onSelect }: { r: LinkRoute; pal: OntologyPalette; onSelect: Props["onSelect"] }) {
  const stroke = r.live ? pal.link : pal.linkIdle;
  const l = r.label;
  return (
    <g
      className="cursor-pointer"
      onClick={(e) => {
        e.stopPropagation();
        onSelect({ kind: "link", name: r.lt.api_name });
      }}
    >
      <rect x={l.x} y={l.y} width={l.w} height={l.h} rx={4} className="fill-card" fillOpacity={0.96} stroke={stroke} strokeOpacity={0.5} />
      <text x={l.x + l.w / 2} y={l.y + 12.5} fontSize={10.5} textAnchor="middle" className={r.live ? "fill-foreground/85" : "fill-muted-foreground"}>
        {l.text}
      </text>
    </g>
  );
}

function ObjectBox({ box, pal, chosen, dim, onSelect }: { box: Box; pal: OntologyPalette; chosen: boolean; dim: boolean; onSelect: Props["onSelect"] }) {
  const color = laneColor(pal, box.colorIndex);
  const { count } = box;
  return (
    <g
      className={cn("group cursor-pointer transition-opacity", dim && "opacity-15")}
      data-object={box.ot.api_name}
      data-unused={count.unused ? "1" : undefined}
      {...pressable(`Object type ${box.ot.api_name}, ${count.text}`, () => onSelect({ kind: "object", name: box.ot.api_name }))}
    >
      <title>{`${box.ot.api_name}: ${count.uncounted ? "not counted" : count.unused ? "declared, and nothing in the store is of this type" : `${count.text} nodes`}`}</title>
      {/* Dashed and unlit: declared, and nothing in the store is of this type.
          Only ever drawn when the count was actually taken. */}
      <rect
        x={box.x}
        y={box.y}
        width={box.w}
        height={box.h}
        rx={8}
        stroke={count.unused ? undefined : color}
        strokeOpacity={count.unused ? 0.8 : 0.6}
        strokeWidth={chosen ? 2.4 : 1}
        strokeDasharray={count.unused ? "5 4" : undefined}
        className={cn(count.unused ? "fill-card/40 stroke-muted-foreground" : "fill-card", "group-hover:stroke-foreground/60")}
      />
      <text x={box.x + 11} y={box.y + 20} fontSize={12} fontWeight={600} className={count.unused ? "fill-muted-foreground" : "fill-foreground"}>
        {box.name}
      </text>
      <text x={box.x + 11} y={box.y + 34} fontSize={10} className="fill-muted-foreground">
        {box.pk}
      </text>
      {box.status && (
        <text x={box.x + box.w - 11} y={box.y + 20} fontSize={9} textAnchor="end" fill={pal.status}>
          {box.status}
        </text>
      )}
      {/* The extra interfaces this type implements, down the left border:
          the bottom-right corner is where the count goes, and a dot sitting
          on the number is a count nobody can read. */}
      {box.dots.map((d) => (
        <circle key={d.iface} cx={d.x} cy={d.y} r={3} fill={laneColor(pal, d.colorIndex)} data-impl={d.iface} />
      ))}
      {count.uncounted ? (
        // Not "0". A count nobody took and a count that came back zero are
        // the same glyph on any page that prints the number either way.
        <text x={box.x + 11} y={box.y + box.h - 11} fontSize={10} fontStyle="italic" className="fill-muted-foreground/80">
          not counted
        </text>
      ) : (
        <>
          <rect x={box.x + 11} y={box.y + box.h - 17} width={box.w - 62} height={6} rx={3} className="fill-muted" />
          {box.bar !== null && <rect x={box.x + 11} y={box.y + box.h - 17} width={Math.max(3, (box.w - 62) * box.bar)} height={6} rx={3} fill={color} fillOpacity={0.8} />}
          <text x={box.x + box.w - 11} y={box.y + box.h - 11} fontSize={10} textAnchor="end" fontStyle={count.unused ? "italic" : undefined} className="fill-muted-foreground tabular-nums">
            {count.text}
          </text>
        </>
      )}
    </g>
  );
}

export function Diagram({ rep, pal, sel, onSelect }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);

  // The layout depends on the width, and an SVG does not reflow the way the
  // text around it does: left alone it keeps whatever width the window had
  // when it was drawn.
  useLayoutEffect(() => {
    const el = host.current;
    if (!el) return;
    const measure = () => setWidth(Math.floor(el.clientWidth - 16));
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const L = useMemo(() => (width > 0 ? layout(rep, width) : null), [rep, width]);
  const lit = useMemo(() => litBy(rep, sel?.kind === "interface" ? sel.name : null), [rep, sel]);
  const chosen = (kind: Selection["kind"], name: string) => sel?.kind === kind && key(sel.name) === key(name);

  const summary = `${rep.object_types.length} object types in ${L?.lanes.length ?? 0} lanes, ${rep.link_types.length} link types`;
  return (
    <div ref={host} className="overflow-x-auto overscroll-x-contain rounded-xl border bg-card p-2" data-diagram>
      {L && (
        <svg
          width={L.width}
          height={L.height}
          viewBox={`0 ${L.top} ${L.width} ${L.height}`}
          role="group"
          aria-label={`The ontology as lanes: ${summary}`}
          className="block select-none font-sans"
          onClick={() => onSelect(null)}
        >
          {L.lanes.map((lane, i) => (
            <LaneShape key={lane.iface?.api_name ?? `loose-${i}`} lane={lane} pal={pal} compact={L.compact} onSelect={onSelect} chosen={!!lane.iface && chosen("interface", lane.iface.api_name)} />
          ))}
          {L.links.map((r) => (
            <LinkCurve key={r.lt.api_name} r={r} pal={pal} chosen={chosen("link", r.lt.api_name)} onSelect={onSelect} />
          ))}
          {Array.from(L.boxes.values()).map((box) => (
            <ObjectBox key={box.ot.api_name} box={box} pal={pal} chosen={chosen("object", box.ot.api_name)} dim={!!lit && !lit.has(key(box.ot.api_name))} onSelect={onSelect} />
          ))}
          {L.links.map((r) => (
            <LinkLabel key={r.lt.api_name} r={r} pal={pal} onSelect={onSelect} />
          ))}
        </svg>
      )}
      {L && L.dangling.length > 0 && (
        <p className="px-2 pb-1 pt-2 text-xs text-muted-foreground">
          {L.dangling.length === 1 ? "One link type names" : `${L.dangling.length} link types name`} a type this schema does not declare, so there is no box to draw it between:{" "}
          {L.dangling.map((l) => l.api_name).join(", ")}.
        </p>
      )}
    </div>
  );
}
