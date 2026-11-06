/**
 * How often the ontology page reads the ontology again: liveview.OntologyInterval,
 * which a Go test holds this to. A schema moves when somebody redesigns the
 * model, so half a minute is already far faster than the thing being watched.
 */
export const ONTOLOGY_MS = 30000;
