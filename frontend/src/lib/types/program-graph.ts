export interface ProgramGraph {
  metadata: {
    name: string;
    displayName: string;
    description: string;
    agentAccess?: boolean;
  };
  configFields: ConfigFieldDef[];
  variables: VariableDef[];
  sections: ProgramSection[];
  outputs: OutputDef[];
}

export interface VariableDef {
  name: string;
  yaml: string; // raw YAML of the value block (indented lines, as found in the source)
}

export interface ConfigFieldDef {
  key: string;
  type: 'string' | 'integer' | 'boolean' | 'number';
  default?: string;
  description?: string;
  group?: string; // UI-only grouping, emitted as a comment in the config block
}

export interface OutputDef {
  key: string;
  value: string;
}

export interface ProgramSection {
  id: string;
  label: string;
  items: ProgramItem[];
}

export type ProgramItem = ResourceItem | LoopItem | ConditionalItem | RawCodeItem;

export interface ResourceItem {
  kind: 'resource';
  name: string;
  resourceType: string;
  properties: PropertyEntry[];
  options?: ResourceOptions;
  rawOptions?: string; // Preserved verbatim when options contain {{ }} template expressions
}

export interface PropertyEntry {
  key: string;
  value: string;
}

export interface ResourceOptions {
  dependsOn?: string[];
  parent?: string;
}

export interface LoopItem {
  kind: 'loop';
  variable: string;
  source: LoopSource;
  serialized: boolean;
  items: ProgramItem[];
}

export type LoopSource =
  | { type: 'until-config'; configKey: string }
  | { type: 'list'; values: string[] }
  | { type: 'raw'; expr: string };

export interface ConditionalItem {
  kind: 'conditional';
  condition: string;
  items: ProgramItem[];
  elseItems?: ProgramItem[];
}

export interface RawCodeItem {
  kind: 'raw';
  yaml: string;
}
