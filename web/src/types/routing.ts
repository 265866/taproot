/**
 * Routing board view model.
 *
 * CPA schedules in strict priority tiers: the highest priority with an available credential
 * serves a request, and `routing.strategy` spreads requests only among that tier. The board is
 * that order made explicit for every OAuth credential.
 */

import type { AuthFileCooldownSnapshot } from './authFile';
import type { RoutingStrategy } from './visualConfig';

export type { RoutingStrategy };

export const ROUTING_STRATEGIES: readonly RoutingStrategy[] = [
  'round-robin',
  'weighted-round-robin',
  'fill-first',
];

export interface RoutingWeightSlot {
  index: number;
  /** Absent weight: CPA runs the credential as 1. */
  weight: number | null;
  /** Credential label, for display only. */
  label: string;
}

export interface RoutingMember {
  key: string;
  /** Credential file name: the identity for writes and the live model list. */
  credentialName: string;
  /** Credential type, for icons and labels. */
  family: string;
  name: string;
  detail: string;
  priority: number;
  weights: RoutingWeightSlot[];
  disabled: boolean;
  /** Runtime-only credentials have no file to store a priority in. */
  locked: boolean;
  prefix: string;
  unavailable: boolean;
  cooldowns: AuthFileCooldownSnapshot | null;
}

export interface RoutingSettings {
  strategy: RoutingStrategy;
  sessionAffinity: boolean;
  requestRetry: number;
  /** 0 tries every available credential. */
  maxRetryCredentials: number;
}

export interface RoutingBoard {
  members: RoutingMember[];
  settings: RoutingSettings;
}

export interface RoutingTier {
  /** Stable while members move, so a tier keeps its identity. */
  id: string;
  memberKeys: string[];
}

export interface RoutingDraft {
  /** Highest priority first. */
  tiers: RoutingTier[];
  /** Per member key, one entry per weight slot. */
  weights: Record<string, (number | null)[]>;
  settings: RoutingSettings;
}

/** A dragged member lands in a tier as a peer, or in the gap before tier `index`. */
export type RoutingDropTarget = { kind: 'tier'; tierId: string } | { kind: 'gap'; index: number };

/** Known model lists, filled in as they load. */
export interface RoutingModelCatalog {
  credentials: Record<string, string[] | null>;
}

export type RoutingChange =
  | { kind: 'priority'; memberKey: string; from: number; to: number }
  | { kind: 'weight'; memberKey: string; index: number; from: number | null; to: number | null }
  | {
      kind: 'setting';
      field: keyof RoutingSettings;
      from: RoutingSettings[keyof RoutingSettings];
      to: RoutingSettings[keyof RoutingSettings];
    };

export interface RoutingPlan {
  changes: RoutingChange[];
  priorities: Record<string, number>;
  weights: Record<string, (number | null)[]>;
  settings: Partial<RoutingSettings>;
}

export type RouteMemberState =
  'serving' | 'excluded' | 'cooling' | 'blocked' | 'unknown' | 'ineligible' | 'disabled';

export type RouteTierState = 'active' | 'standby' | 'skipped' | 'cooling' | 'unknown';

export interface RouteMember {
  key: string;
  state: RouteMemberState;
  /** Share of the tier's traffic in percent; null when the strategy has no fixed split. */
  share: number | null;
}

export interface RouteTier {
  tierId: string;
  state: RouteTierState;
  members: RouteMember[];
}

export interface RoutePreview {
  tiers: RouteTier[];
  hasActive: boolean;
}
