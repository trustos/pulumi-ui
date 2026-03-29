import { describe, it, expect } from 'vitest';
import { solutions } from './solutions';

describe('NocoBase solution', () => {
  const nocobase = solutions.find(s => s.id === 'nocobase')!;

  it('exists', () => {
    expect(nocobase).toBeDefined();
    expect(nocobase.program).toBe('nomad-cluster');
  });

  it('pre-selects correct applications', () => {
    expect(nocobase.applications).toContain('traefik');
    expect(nocobase.applications).toContain('postgres');
    expect(nocobase.applications).toContain('postgres-backup');
    expect(nocobase.applications).toContain('nocobase');
  });

  it('derives config with all infrastructure defaults', () => {
    const result = nocobase.deriveConfig({ email: 'admin@example.com' });

    expect(result.config.nodeCount).toBe('1');
    expect(result.config.compartmentName).toBe('nomad-compartment');
    expect(result.config.vcnCidr).toBe('10.0.0.0/16');
    expect(result.config.shape).toBe('VM.Standard.A1.Flex');
    expect(result.config.nomadVersion).toBeTruthy();
    expect(result.config.consulVersion).toBeTruthy();
  });

  it('derives appConfig from email', () => {
    const result = nocobase.deriveConfig({ email: 'admin@test.com' });

    expect(result.appConfig['traefik.acmeEmail']).toBe('admin@test.com');
    expect(result.appConfig['postgres.pgadminEmail']).toBe('admin@test.com');
    expect(result.appConfig['nocobase.dbUser']).toBe('admin');
    expect(result.appConfig['nocobase.dbName']).toBe('nocobase');
  });

  it('selects all required applications', () => {
    const result = nocobase.deriveConfig({ email: 'x@x.com' });

    expect(result.applications.traefik).toBe(true);
    expect(result.applications.postgres).toBe(true);
    expect(result.applications['postgres-backup']).toBe(true);
    expect(result.applications.nocobase).toBe(true);
  });

  it('requires only email field', () => {
    const required = nocobase.userFields.filter(f => f.required);
    expect(required).toHaveLength(1);
    expect(required[0].key).toBe('email');
  });
});

describe('Nomad Cluster solution', () => {
  const nomad = solutions.find(s => s.id === 'nomad-cluster')!;

  it('exists', () => {
    expect(nomad).toBeDefined();
    expect(nomad.program).toBe('nomad-cluster');
  });

  it('only pre-selects traefik', () => {
    expect(nomad.applications).toEqual(['traefik']);
  });

  it('derives config with 3 nodes by default', () => {
    const result = nomad.deriveConfig({ email: 'x@x.com' });
    expect(result.config.nodeCount).toBe('3');
  });

  it('passes email as acmeEmail', () => {
    const result = nomad.deriveConfig({ email: 'ops@company.com' });
    expect(result.appConfig['traefik.acmeEmail']).toBe('ops@company.com');
  });
});
