// Shared types matching the JSON shape emitted by `cupel build-data`
// (cmd/cupel/build_data.go: buildDataCardJSON, buildDataEngineSpecJSON).

export interface EngineTag {
  name: string;
  role: string;
  tier: string;
  excluded?: boolean;
}

export interface Work {
  slug: string;
  work: string;
  author: string;
  year: string;
  medium: string;
  backing: string;
  source: string;
  translator?: string;
  author_note?: string;
  bead: string;
  bead_html?: string;
  engines: EngineTag[];
  has_evidence: boolean;
  reading_html: string;
  full_html: string;
}

export interface EngineWorkRef {
  work: string;
  href: string;
}

export interface EngineSpec {
  name: string;
  slug: string;
  tagline?: string;
  body_html: string;
  works?: EngineWorkRef[];
}

export interface ClusterSpec {
  row_number: number;
  name: string;
  slug: string;
  candidate?: boolean;
  domain: string;
  intro_html?: string;
  status_prose: string;
  engines_prose: string;
  specimens_prose: string;
  status_html: string;
  engines_html: string;
  specimens_html: string;
}

export interface TheoryDoc {
  slug: string;
  title: string;
  body_html: string;
}

export interface GlossaryEntry {
  section: string;
  term: string;
  aliases?: string[];
  slug: string;
  definition_html: string;
}
