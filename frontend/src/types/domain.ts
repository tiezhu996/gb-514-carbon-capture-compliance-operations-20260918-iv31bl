
export interface DomainRecord {
  id: number;
  code: string;
  name: string;
  status: string;
  version: number;
  description: string;
  facility: string;
  owner: string;
  category: string;
  riskLevel: 'low' | 'medium' | 'high' | 'critical';
  metricValue: number;
  metricUnit: string;
  effectiveAt: string;
  evidence: string;
  relatedCode: string;
  createdAt: string;
  updatedAt: string;
  revisions?: DecisionRevision[];
}

export interface DecisionRevision {
  id: number;
  complianceDecisionId: number;
  version: number;
  state: string;
  evidence: string;
  reason: string;
  actor: string;
  requestId: string;
  createdAt: string;
}

export interface ReviewReference {
  id: number;
  code: string;
  name: string;
  status: string;
  facility: string;
  metricValue: number;
  metricUnit: string;
  version: number;
}

// ReviewCheck mirrors backend dto.ReviewCheck: the re-read permit rule and
// latest verified sample at the moment a decision enters review or reaches a
// final judgement. blocked/overThreshold and reasons drive the page gating.
export interface ReviewCheck {
  decisionId: number;
  decisionCode: string;
  relatedCode: string;
  currentState: string;
  unit?: ReviewReference;
  permitRule?: ReviewReference;
  sample?: ReviewReference;
  thresholdValue: number;
  thresholdUnit: string;
  sampleValue: number;
  sampleUnit: string;
  overThreshold: boolean;
  blocked: boolean;
  reasons: string[];
  allowedTargets: string[];
  checkedAt: string;
}

export interface PageMeta { page: number; pageSize: number; total: number }
export interface ApiEnvelope<T> { data: T; error?: string; message?: string; meta?: PageMeta }
export interface UserSession { token: string; username: string; displayName: string; role: string; expiresIn: number }
export interface AuditLog {
  id: number; requestId: string; actor: string; action: string; entityType: string;
  entityId: number; beforeState: string; afterState: string; detail: string; createdAt: string;
}
export interface EntityConfig { key: string; path: string; label: string; statuses: readonly string[] }
