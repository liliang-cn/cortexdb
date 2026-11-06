import type { OntologySchemaRef } from "@/api/ontologyHistory";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";

/** Which schema's versions to list: every saved one, with the active one marked. */
export function SchemaPicker({ value, options, onChange }: { value: string; options: OntologySchemaRef[]; onChange: (schema: string) => void }) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger size="sm" aria-label="Schema" className="max-w-[16rem] min-w-[9rem]">
        <SelectValue placeholder="Schema" />
      </SelectTrigger>
      <SelectContent position="popper" align="end">
        {options.map((s) => (
          <SelectItem key={s.schema_id} value={s.schema_id}>
            <span className="font-mono text-[0.8rem]">{s.schema_id}</span>
            {s.active && <span className="text-xs text-op-added-ink">active</span>}
            {s.version === 0 && <span className="text-xs text-muted-foreground">not saved now</span>}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
