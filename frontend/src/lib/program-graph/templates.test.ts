import { describe, it, expect } from 'vitest';
import { yamlToGraph } from './parser';
import type { ProgramItem } from '$lib/types/program-graph';

import singleInstanceYaml from './templates/single-instance.yaml?raw';
import haPairYaml from './templates/ha-pair.yaml?raw';
import loadBalancedClusterYaml from './templates/load-balanced-cluster.yaml?raw';
import orchestratorClusterYaml from './templates/orchestrator-cluster.yaml?raw';
import bastionHostYaml from './templates/bastion-host.yaml?raw';
import databaseServerYaml from './templates/database-server.yaml?raw';
import webServerYaml from './templates/web-server.yaml?raw';
import devEnvironmentYaml from './templates/dev-environment.yaml?raw';
import multiTierAppYaml from './templates/multi-tier-app.yaml?raw';
import vcnOnlyYaml from './templates/vcn-only.yaml?raw';
import privateSubnetYaml from './templates/private-subnet.yaml?raw';

// ── helpers ───────────────────────────────────────────────────────────────────

function countResources(items: ProgramItem[]): number {
  let n = 0;
  for (const item of items) {
    if (item.kind === 'resource') n++;
    else if (item.kind === 'loop') n += countResources(item.items);
    else if (item.kind === 'conditional') {
      n += countResources(item.items);
      if (item.elseItems) n += countResources(item.elseItems);
    }
  }
  return n;
}

function findAD(items: ProgramItem[]): string | undefined {
  for (const item of items) {
    if (item.kind === 'resource') {
      const p = item.properties.find(p => p.key === 'availabilityDomain');
      if (p) return p.value;
    }
    if (item.kind === 'loop') {
      const v = findAD(item.items);
      if (v !== undefined) return v;
    }
  }
}

// ── parse without errors ──────────────────────────────────────────────────────

describe('built-in YAML templates — parse without errors', () => {
  it('single-instance parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(singleInstanceYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('single-instance');
  });

  it('ha-pair parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(haPairYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('ha-pair');
  });

  it('load-balanced-cluster parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(loadBalancedClusterYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('load-balanced-cluster');
  });

  it('orchestrator-cluster parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(orchestratorClusterYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('orchestrator-cluster');
  });

  it('bastion-host parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(bastionHostYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('bastion-host');
  });

  it('database-server parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(databaseServerYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('database-server');
  });

  it('web-server parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(webServerYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('web-server');
  });

  it('dev-environment parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(devEnvironmentYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('dev-environment');
  });

  it('multi-tier-app parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(multiTierAppYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('multi-tier-app');
  });

  it('vcn-only parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(vcnOnlyYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('my-vcn');
  });

  it('private-subnet parses cleanly', () => {
    const { graph, degraded } = yamlToGraph(privateSubnetYaml);
    expect(degraded).toBe(false);
    expect(graph.sections.length).toBeGreaterThan(0);
    expect(graph.metadata.name).toBe('private-subnet');
  });
});

// ── meta.displayName parsed ───────────────────────────────────────────────────

describe('built-in YAML templates — meta.displayName parsed', () => {
  it('single-instance has displayName "Single Compute Instance"', () => {
    const { graph } = yamlToGraph(singleInstanceYaml);
    expect(graph.metadata.displayName).toBe('Single Compute Instance');
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });

  it('ha-pair has displayName "High-Availability Pair with NLB"', () => {
    const { graph } = yamlToGraph(haPairYaml);
    expect(graph.metadata.displayName).toBe('High-Availability Pair with NLB');
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });

  it('load-balanced-cluster has displayName "Load-Balanced Application Cluster"', () => {
    const { graph } = yamlToGraph(loadBalancedClusterYaml);
    expect(graph.metadata.displayName).toBe('Load-Balanced Application Cluster');
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });

  it('orchestrator-cluster has displayName "Container Orchestration Cluster"', () => {
    const { graph } = yamlToGraph(orchestratorClusterYaml);
    expect(graph.metadata.displayName).toBe('Container Orchestration Cluster');
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });

  it('bastion-host has a non-empty displayName different from name', () => {
    const { graph } = yamlToGraph(bastionHostYaml);
    expect(graph.metadata.displayName).toBeTruthy();
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });

  it('database-server has a non-empty displayName different from name', () => {
    const { graph } = yamlToGraph(databaseServerYaml);
    expect(graph.metadata.displayName).toBeTruthy();
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });

  it('web-server has a non-empty displayName different from name', () => {
    const { graph } = yamlToGraph(webServerYaml);
    expect(graph.metadata.displayName).toBeTruthy();
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });

  it('dev-environment has a non-empty displayName different from name', () => {
    const { graph } = yamlToGraph(devEnvironmentYaml);
    expect(graph.metadata.displayName).toBeTruthy();
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });

  it('multi-tier-app has a non-empty displayName different from name', () => {
    const { graph } = yamlToGraph(multiTierAppYaml);
    expect(graph.metadata.displayName).toBeTruthy();
    expect(graph.metadata.displayName).not.toBe(graph.metadata.name);
  });
});

// ── availabilityDomain normalizes to @auto ────────────────────────────────────

describe('built-in YAML templates — availabilityDomain normalizes to @auto', () => {
  it('single-instance: standalone instance availabilityDomain is @auto', () => {
    const { graph } = yamlToGraph(singleInstanceYaml);
    const allItems = graph.sections.flatMap(s => s.items);
    const ad = findAD(allItems);
    expect(ad).toBe('@auto');
  });

  it('ha-pair: first instance availabilityDomain is @auto', () => {
    const { graph } = yamlToGraph(haPairYaml);
    const allItems = graph.sections.flatMap(s => s.items);
    const ad = findAD(allItems);
    expect(ad).toBe('@auto');
  });

  it('load-balanced-cluster: loop instance availabilityDomain is @auto', () => {
    const { graph } = yamlToGraph(loadBalancedClusterYaml);
    const allItems = graph.sections.flatMap(s => s.items);
    const ad = findAD(allItems);
    expect(ad).toBe('@auto');
  });
});

// ── agentAccess flag ──────────────────────────────────────────────────────────

describe('built-in YAML templates — agentAccess flag', () => {
  it('ha-pair has agentAccess === true', () => {
    const { graph } = yamlToGraph(haPairYaml);
    expect(graph.metadata.agentAccess).toBe(true);
  });

  it('load-balanced-cluster has agentAccess === true', () => {
    const { graph } = yamlToGraph(loadBalancedClusterYaml);
    expect(graph.metadata.agentAccess).toBe(true);
  });

  it('orchestrator-cluster has agentAccess === true', () => {
    const { graph } = yamlToGraph(orchestratorClusterYaml);
    expect(graph.metadata.agentAccess).toBe(true);
  });

  it('bastion-host does NOT have agentAccess (private instance, no NLB)', () => {
    const { graph } = yamlToGraph(bastionHostYaml);
    expect(graph.metadata.agentAccess).toBeFalsy();
  });

  it('database-server does NOT have agentAccess (private, no NLB)', () => {
    const { graph } = yamlToGraph(databaseServerYaml);
    expect(graph.metadata.agentAccess).toBeFalsy();
  });

  it('web-server has agentAccess === true', () => {
    const { graph } = yamlToGraph(webServerYaml);
    expect(graph.metadata.agentAccess).toBe(true);
  });

  it('dev-environment has agentAccess === true', () => {
    const { graph } = yamlToGraph(devEnvironmentYaml);
    expect(graph.metadata.agentAccess).toBe(true);
  });

  it('multi-tier-app does NOT have agentAccess (private tiers, no NLB)', () => {
    const { graph } = yamlToGraph(multiTierAppYaml);
    expect(graph.metadata.agentAccess).toBeFalsy();
  });

  it('single-instance does NOT have agentAccess', () => {
    const { graph } = yamlToGraph(singleInstanceYaml);
    expect(graph.metadata.agentAccess).toBeFalsy();
  });

  it('vcn-only does NOT have agentAccess', () => {
    const { graph } = yamlToGraph(vcnOnlyYaml);
    expect(graph.metadata.agentAccess).toBeFalsy();
  });

  it('private-subnet does NOT have agentAccess', () => {
    const { graph } = yamlToGraph(privateSubnetYaml);
    expect(graph.metadata.agentAccess).toBeFalsy();
  });
});

// ── resource counts ───────────────────────────────────────────────────────────

describe('built-in YAML templates — resource counts', () => {
  it('vcn-only has exactly 1 resource in 1 section', () => {
    const { graph } = yamlToGraph(vcnOnlyYaml);
    expect(graph.sections).toHaveLength(1);
    const total = countResources(graph.sections[0].items);
    expect(total).toBe(1);
  });

  it('single-instance has 6 resources across 2 sections (5 networking + 1 instance)', () => {
    const { graph } = yamlToGraph(singleInstanceYaml);
    expect(graph.sections).toHaveLength(2);
    const total = graph.sections.reduce((sum, s) => sum + countResources(s.items), 0);
    expect(total).toBe(6);
  });

  it('ha-pair has more than 5 resources', () => {
    const { graph } = yamlToGraph(haPairYaml);
    const total = graph.sections.reduce((sum, s) => sum + countResources(s.items), 0);
    expect(total).toBeGreaterThan(5);
  });
});
