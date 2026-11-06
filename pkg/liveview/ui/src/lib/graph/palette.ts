// The scene's colours that come from the app's tokens. WebGL cannot read a
// stylesheet, so the variables are resolved to hex here, once per theme change.

import type { ChangeOp } from "@/api";
import { ACTIVITY_ORDER, CHANGE_OP_ORDER, GRADE_ORDER, type ActivityTone, type GradeKey } from "@/lib/semantics";

export interface ScenePalette {
  dark: boolean;
  background: string;
  foreground: string;
  muted: string;
  ops: Record<ChangeOp, string>;
  kinds: Record<ActivityTone, string>;
  grades: Record<GradeKey, string>;
}

const kebab = (s: string) => s.replaceAll("_", "-");

function cssVar(style: CSSStyleDeclaration, name: string, fallback: string): string {
  const v = style.getPropertyValue(name).trim();
  return /^#[0-9a-f]{6}$/i.test(v) ? v : fallback;
}

export function readPalette(root: HTMLElement = document.documentElement): ScenePalette {
  const style = getComputedStyle(root);
  const dark = root.classList.contains("dark");
  const ops = {} as Record<ChangeOp, string>;
  for (const k of CHANGE_OP_ORDER) ops[k] = cssVar(style, `--op-${k}`, "#64748b");
  const kinds = {} as Record<ActivityTone, string>;
  for (const k of ACTIVITY_ORDER) kinds[k] = cssVar(style, `--kind-${k}`, "#4f46e5");
  const grades = {} as Record<GradeKey, string>;
  for (const k of GRADE_ORDER) grades[k] = cssVar(style, `--grade-${kebab(k)}`, "#94a3b8");
  return {
    dark,
    background: cssVar(style, "--background", dark ? "#0d1016" : "#f6f7f9"),
    foreground: cssVar(style, "--foreground", dark ? "#e8ebf1" : "#12161d"),
    muted: cssVar(style, "--muted-foreground", "#5d6678"),
    ops,
    kinds,
    grades,
  };
}
