/**
 * The contract with the engine.
 *
 * Every shape here has a Go counterpart, and the pairing is the point: these
 * are the only places the two languages have to agree, so they are written down
 * once and imported everywhere rather than re-guessed per call site.
 *
 *   Field          internal/fields.Field
 *   Fit            internal/server.Fit
 *   Template       the map internal/server.describe() builds
 *   HistoryEntry   internal/store.Entry
 *
 * When one of these is wrong the failure is a silent blank in the interface —
 * a control that does not render, a fit line that says undefined. Typing them
 * turns that into a compile error.
 */

/** The closed vocabulary of field kinds. Mirrors `fields.Kind` exactly. */
export type FieldKind =
  | 'text' | 'rich' | 'icon' | 'number'
  | 'bool' | 'enum' | 'photo' | 'list' | 'group';

export interface EnumValue {
  value: string;
  label: string;
  i18n?: string;
}

/**
 * One field of the document, as the engine describes it.
 *
 * A flat shape with a `kind` that selects which members are read — the same
 * choice `fields.Field` makes in Go, and for the same reason: the vocabulary is
 * closed, so a flat record makes it checkable at a glance and makes a property
 * set on the wrong kind ignored rather than misread.
 */
export interface Field {
  kind: FieldKind;
  key: string;
  label: string;

  i18n?: string;
  required?: boolean;
  help?: string;
  helpI18n?: string;
  placeholder?: string;
  placeholderI18n?: string;
  widget?: string;

  /** text, rich, icon, photo: a length. number: an upper bound. */
  max?: number;
  /** number: a lower bound. list: a required count. */
  min?: number;
  /** rich: how many rows the control gets. */
  rows?: number;

  values?: EnumValue[];
  default?: string;

  /** list: the element type. */
  of?: Field;
  /** group: the members. */
  fields?: Field[];

  addLabel?: string;
  addI18n?: string;
}

/** A kind of section a template can draw, with the fields it is made of. */
export interface SectionSlot {
  type: string;
  column: string;
  max?: number;
  label?: string;
  i18n?: string;
  fields: Field[];
}

/** A template, described well enough for an editor to be built from it. */
export interface Template {
  uuid: string;
  name: string;
  title: string;
  description?: string;
  columns: string[];
  cover?: string;
  icons: string[];
  identity: Field[];
  meta: Field[];
  sections: SectionSlot[];
}

/** A template as the pickers see it: no field trees, nothing personal. */
export interface TemplateSummary {
  uuid: string;
  name: string;
  title: string;
  description?: string;
}

/**
 * A CV, as it travels.
 *
 * Deliberately NOT a struct all the way down. The engine keeps properties it
 * does not recognise so that a document written by a newer version still opens
 * in an older one, and a type that named every field would invite code that
 * drops the rest on the next save. `identity` and `sections` are typed because
 * the interface genuinely reads them; everything else stays open.
 */
export interface CvIdentity {
  name?: string;
  role?: string;
  photo?: string;
  photoAlt?: string;
  subtitle?: Array<{ text?: string; strong?: boolean }>;
  [key: string]: unknown;
}

export interface CvSection {
  id: string;
  type: string;
  column: string;
  title?: string;
  [key: string]: unknown;
}

export interface CvContent {
  identity: CvIdentity;
  sections: CvSection[];
  [key: string]: unknown;
}

export interface CvMeta {
  lang?: string;
  template?: string;
  documentTitle?: string;
  updatedAt?: string;
  [key: string]: unknown;
}

export interface Cv {
  meta: CvMeta;
  content: CvContent;
  [key: string]: unknown;
}

/** One of the documents a CV exists as. */
export interface LanguageEntry {
  lang: string;
  variant?: string;
  isDefault: boolean;
}

/** What the engine said about the page. */
export interface FitReportData {
  ok: boolean;
  summary: string;
  margins: Record<string, number>;
  over?: string[];
  /** The tightest and loosest spacing any column was set at. */
  spacing: [number, number];
  /** The type scale, 1 unless spacing alone could not make the page hold. */
  text: number;
}

/** One step of the trail naming a field in the journal. */
export interface Crumb {
  label: string;
  i18n?: string;
  /** Which one, when the step addresses a list element. One-based. */
  n?: number;
}

export type HistoryKind = 'set' | 'add' | 'remove' | 'move';

export interface HistoryEntry {
  at: string;
  from: string;
  lang?: string;
  path: string;
  kind: HistoryKind;
  trail: Crumb[];
  before: string | number | boolean | null;
  after: string | number | boolean | null;
  count: number;
}

// --- what each route answers ------------------------------------------------

export interface Answer {
  ok: true;
}

export interface LoadAnswer extends Answer {
  slug: string;
  doc: Cv;
  template: Template;
  languages: LanguageEntry[];
  fit: FitReportData;
}

/**
 * A save answers with the stored document and nothing else.
 *
 * No fit: that is a layout, and a keystroke must not pay for one. The page and
 * what the engine thought of it come from render(), separately.
 */
export interface SaveAnswer extends Answer {
  doc: Cv;
}

export interface PreviewAnswer extends Answer {
  html: string;
  fit: FitReportData;
}

export interface HistoryAnswer extends Answer {
  entries: HistoryEntry[];
}

export interface LinksAnswer extends Answer {
  read: string;
}

export interface DeleteAnswer extends Answer {
  slug: string;
  /** Hours the CV can still be restored for. */
  grace: number;
}

export interface TemplatesAnswer extends Answer {
  templates: TemplateSummary[];
}

export interface LanguagesAnswer extends Answer {
  languages: LanguageEntry[];
}

// --- the administration port ------------------------------------------------

export interface ProfileLinks {
  edit: string;
  read: string;
  createdAt?: string;
}

export interface ProfileSummary {
  slug: string;
  name: string;
  public: boolean;
  languages: LanguageEntry[];
  bytes: number;
  history: number;
  links: ProfileLinks;
  updatedAt?: string;
}

export interface InventoryAnswer extends Answer {
  publicUrl: string;
  policy: string;
  default: string;
  total: number;
  page: number;
  pages: number;
  profiles: ProfileSummary[];
}

/** What the service has been doing. See internal/metrics for why each is here. */
export interface MetricsAnswer extends Answer {
  metrics: {
    version: string;
    uptimeSec: number;
    requests: number;
    renders: number;
    pdfs: number;
    saves: number;
    errors: number;
    conflicts: number;
    refused: number;
    cacheHits: number;
    cacheMisses: number;
    cacheRatio: number;
    renderMedianMs: number;
    renderSlowMs: number;
    lastBackup?: string;
  };
  profiles: number;
  bytes: number;
  cacheBytes: number;
  cacheCount: number;
  editing: number;
  domain: string;
  policy: string;
  guarded: boolean;
  challenge: boolean;
  limits: { profileMB: number; freeMB: number };
}

export interface TrashEntry {
  slug: string;
  name: string;
  deletedAt: string;
  expiresAt: string;
  bytes: number;
}

export interface TrashAnswer extends Answer {
  entries: TrashEntry[];
}

export interface CreateAnswer extends Answer {
  profile: ProfileSummary;
}

export interface AdminTemplatesAnswer extends Answer {
  templates: TemplateSummary[];
}
